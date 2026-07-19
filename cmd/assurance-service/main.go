package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"github.com/herdifirdausss/seev/internal/assurance"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/alerting"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/grpcx"
	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/tracing"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe assurance-service liveness endpoint")
	flag.Parse()
	if *healthcheck {
		if err := probeHealth(os.Getenv); err != nil {
			slog.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(context.Background()); err != nil {
		slog.Error("assurance-service stopped", "error", err)
		os.Exit(1)
	}
}

func probeHealth(getenv func(string) string) error {
	port := getenv("APP_PORT")
	if port == "" {
		port = "8096"
	}
	response, err := (&http.Client{Timeout: 3 * time.Second}).Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}

func run(parent context.Context) error {
	cfg, err := config.LoadAssuranceService()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if os.Getenv("APP_PORT") == "" {
		cfg.App.Port = "8096"
	}
	log := logger.New(cfg.Logger.Pkg())
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTracing, err := tracing.Setup(ctx, tracing.Config{ServiceName: "assurance-service", Endpoint: cfg.Tracing.OTLPEndpoint, SampleRatio: cfg.Tracing.SampleRatio, Insecure: cfg.Tracing.Insecure})
	if err != nil {
		log.Error("tracing: setup failed, continuing without a tracer provider", "error", err)
		shutdownTracing = func(context.Context) error { return nil }
	}
	db, err := database.New(ctx, cfg.Postgres.Pkg())
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer db.Close()
	payinConn, err := grpcx.DialLazy(ctx, cfg.PayinGRPCAddr, cfg.InternalGRPCToken)
	if err != nil {
		return fmt.Errorf("dial payin: %w", err)
	}
	defer payinConn.Close()
	payoutConn, err := grpcx.DialLazy(ctx, cfg.PayoutGRPCAddr, cfg.InternalGRPCToken)
	if err != nil {
		return fmt.Errorf("dial payout: %w", err)
	}
	defer payoutConn.Close()
	ledgerConn, err := grpcx.DialLazy(ctx, cfg.LedgerGRPCAddr, cfg.InternalGRPCToken)
	if err != nil {
		return fmt.Errorf("dial ledger: %w", err)
	}
	defer ledgerConn.Close()
	var alertFn alerting.AlertFunc
	if cfg.Assurance.AlertWebhookURL != "" {
		alertFn = alerting.NewWebhookAlerter(cfg.Assurance.AlertWebhookURL, nil)
	}
	module := assurance.NewModule(db, cfg.Assurance, payinv1.NewPayinServiceClient(payinConn), payoutv1.NewPayoutServiceClient(payoutConn), ledgerv1.NewLedgerServiceClient(ledgerConn), alertFn, log)
	module.Start(ctx)
	defer module.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		if err := db.HealthCheck(r.Context()); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	mux.Handle("GET /metrics", promhttp.Handler())
	server := &http.Server{Addr: ":" + cfg.App.Port, Handler: mux, ReadTimeout: cfg.App.ReadTimeout, WriteTimeout: cfg.App.WriteTimeout, IdleTimeout: cfg.App.IdleTimeout, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 1 << 20}
	errCh := make(chan error, 1)
	go func() {
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()
	log.Info("assurance-service started", "http", server.Addr, "consistency_delay", cfg.Assurance.ConsistencyDelay, "interval", cfg.Assurance.Interval)
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errCh:
		cancel()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = err
	}
	if err := shutdownTracing(context.Background()); err != nil && serveErr == nil {
		serveErr = err
	}
	return serveErr
}
