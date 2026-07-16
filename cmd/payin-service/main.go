package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	fraudv1 "github.com/herdifirdausss/seev/gen/fraud/v1"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/payin"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/internal/vendorgw/mockvendor"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/grpcx"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/logger"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the payin-service liveness endpoint")
	flag.Parse()
	if *healthcheck {
		if err := probeHealth(os.Getenv); err != nil {
			slog.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(context.Background()); err != nil {
		slog.Error("payin-service stopped", "error", err)
		os.Exit(1)
	}
}

func probeHealth(getenv func(string) string) error {
	port := getenv("APP_PORT")
	if port == "" {
		port = "8092"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", res.Status)
	}
	return nil
}

func run(parent context.Context) error {
	cfg, err := config.LoadPayinService()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if os.Getenv("APP_PORT") == "" {
		cfg.App.Port = "8092"
	}
	if os.Getenv("GRPC_PORT") == "" {
		cfg.GRPCPort = "9092"
	}
	log := logger.New(cfg.Logger.Pkg())
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	db, err := database.New(ctx, cfg.Postgres.Pkg())
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	ledgerConn, err := grpcx.Dial(ctx, cfg.LedgerGRPCAddr, cfg.InternalGRPCToken)
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("connect ledger-service: %w", err)
	}
	registry := vendorgw.NewRegistry()
	if cfg.Vendor.MockvendorEnabled {
		registry.AddPayin(mockvendor.New(cfg.Vendor.MockvendorSecret))
		log.Warn("vendorgw: mockvendor enabled — test-only vendor")
	}

	// fraud client screens deposits pre-posting (docs/plan/37 Task T4).
	// FRAUD_GRPC_ADDR unset (dev/test defaults) => nil client => no screening.
	var fraudClient *fraudcheck.Client
	var fraudConn *grpc.ClientConn
	if cfg.FraudGRPCAddr != "" {
		fraudConn, err = grpcx.DialLazy(ctx, cfg.FraudGRPCAddr, cfg.InternalGRPCToken)
		if err != nil {
			_ = ledgerConn.Close()
			_ = db.Close()
			return fmt.Errorf("create fraud-service client: %w", err)
		}
		defer func() { _ = fraudConn.Close() }()
		fraudClient = fraudcheck.New(fraudv1.NewFraudServiceClient(fraudConn), "payin")
	}

	module := payin.NewModule(db, ledgerclient.New(ledgerConn), registry, cfg.Vendor.TopupIntentTTL, log, fraudClient)
	grpcServer := grpcx.NewServer(log, cfg.InternalGRPCToken)
	module.RegisterGRPC(grpcServer)
	grpcListener, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		_ = ledgerConn.Close()
		_ = db.Close()
		return fmt.Errorf("listen grpc: %w", err)
	}
	httpServer := &http.Server{Addr: ":" + cfg.App.Port, Handler: adminRouter(cfg, module, log), ReadTimeout: cfg.App.ReadTimeout, WriteTimeout: cfg.App.WriteTimeout, IdleTimeout: cfg.App.IdleTimeout, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 1 << 20}
	errCh := make(chan error, 2)
	go serveGRPC(grpcServer, grpcListener, errCh)
	go serveHTTP(httpServer, errCh)
	log.Info("payin-service started", "grpc", grpcListener.Addr(), "admin_http", httpServer.Addr)
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errCh:
		cancel()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = err
	}
	gracefulStopGRPC(grpcServer, cfg.App.ShutdownTimeout)
	if err := ledgerConn.Close(); err != nil {
		log.Error("close ledger grpc", "error", err)
	}
	if err := db.Close(); err != nil {
		log.Error("close postgres", "error", err)
	}
	return serveErr
}

func serveHTTP(server *http.Server, errCh chan<- error) {
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- err
	}
}
func serveGRPC(server *grpc.Server, listener net.Listener, errCh chan<- error) {
	if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		errCh <- err
	}
}
func gracefulStopGRPC(server *grpc.Server, timeout time.Duration) {
	done := make(chan struct{})
	go func() { server.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		server.Stop()
	}
}
