package main

import (
	"context"
	"encoding/json"
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
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"github.com/herdifirdausss/seev/internal/assurance"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/alerting"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/grpcx"
	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/tlsx"
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
	certDir := getenv("TLS_CERT_DIR")
	if certDir == "" {
		certDir = "deploy/certs"
	}
	certSrc, err := tlsx.LoadFromDir(certDir, "dev-operator", slog.Default())
	if err != nil {
		return fmt.Errorf("load healthcheck TLS identity: %w", err)
	}
	defer certSrc.Stop()
	response, err := tlsx.HTTPClient(certSrc, tlsx.IdentityAssurance, 3*time.Second).Get("https://127.0.0.1:" + port + "/health")
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
	// Assurance is deliberately a small read-only worker. Keep its pool bounded
	// even if a global POSTGRES_MAX_OPEN_CONNS override is larger for domain
	// services; five connections is the Plan 48 safety ceiling.
	if cfg.Postgres.MaxOpenConns > 5 || cfg.Postgres.MaxOpenConns <= 0 {
		cfg.Postgres.MaxOpenConns = 5
	}
	if cfg.Postgres.MaxIdleConns > 5 || cfg.Postgres.MaxIdleConns <= 0 {
		cfg.Postgres.MaxIdleConns = 5
	}
	log := logger.New(cfg.Logger.Pkg())
	// docs/plan/49 K3/K5: load this process's own identity + the shared
	// CA before anything else. assurance-service itself was added to the
	// repo after doc 49's K3/K4/K6 were written — see docs/security/
	// threat-model.md TM-09 — so it is treated exactly like every other
	// gRPC client here despite not being enumerated in the original doc.
	certSrc, err := tlsx.LoadFromDir(cfg.TLSCertDir, "assurance", log)
	if err != nil {
		return fmt.Errorf("load TLS certificates: %w", err)
	}
	defer certSrc.Stop()
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
	payinConn, err := grpcx.DialLazy(ctx, cfg.PayinGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityPayin))
	if err != nil {
		return fmt.Errorf("dial payin: %w", err)
	}
	defer payinConn.Close()
	payoutConn, err := grpcx.DialLazy(ctx, cfg.PayoutGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityPayout))
	if err != nil {
		return fmt.Errorf("dial payout: %w", err)
	}
	defer payoutConn.Close()
	ledgerConn, err := grpcx.DialLazy(ctx, cfg.LedgerGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityLedger))
	if err != nil {
		return fmt.Errorf("dial ledger: %w", err)
	}
	defer ledgerConn.Close()
	var alertFn alerting.AlertFunc
	if cfg.Assurance.AlertWebhookURL != "" {
		alertFn = alerting.NewWebhookAlerterForService(cfg.Assurance.AlertWebhookURL, "seev-assurance", nil)
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
		components := map[string]string{"postgres": "ok", "payin": "ok", "payout": "ok", "ledger": "ok"}
		ready := true
		if err := db.HealthCheck(r.Context()); err != nil {
			components["postgres"], ready = "unavailable", false
		}
		for name, conn := range map[string]*grpc.ClientConn{"payin": payinConn, "payout": payoutConn, "ledger": ledgerConn} {
			checkCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			_, err := healthpb.NewHealthClient(conn).Check(checkCtx, &healthpb.HealthCheckRequest{})
			cancel()
			if err != nil {
				components[name], ready = "unavailable", false
			}
		}
		if !ready {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "degraded", "components": components})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	mux.Handle("GET /metrics", promhttp.Handler())
	authed := middleware.Chain(middleware.WithAuth(cfg.JWT.Secret, cfg.JWT.Issuer), middleware.RequireJSON())
	mux.Handle("/admin/assurance/", authed(module.AdminRouter()))
	// docs/plan/49 K6: assurance's admin listener is internal-only mTLS —
	// no other service dials it over HTTP, only dev-operator/Prometheus.
	server := &http.Server{Addr: ":" + cfg.App.Port, Handler: mux, ReadTimeout: cfg.App.ReadTimeout, WriteTimeout: cfg.App.WriteTimeout, IdleTimeout: cfg.App.IdleTimeout, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 1 << 20, TLSConfig: tlsx.ServerConfig(certSrc, []string{
		tlsx.IdentityDevOperator, tlsx.IdentityPrometheus,
	})}
	errCh := make(chan error, 1)
	go func() {
		var serveErr error
		if server.TLSConfig != nil {
			serveErr = server.ListenAndServeTLS("", "")
		} else {
			serveErr = server.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
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
