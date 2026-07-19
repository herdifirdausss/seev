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

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	fraudv1 "github.com/herdifirdausss/seev/gen/fraud/v1"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/payout"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/internal/vendorgw/mockvendor"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/grpcx"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/tracing"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe payout-service liveness endpoint")
	flag.Parse()
	if *healthcheck {
		if err := probeHealth(os.Getenv); err != nil {
			slog.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(context.Background()); err != nil {
		slog.Error("payout-service stopped", "error", err)
		os.Exit(1)
	}
}
func probeHealth(getenv func(string) string) error {
	port := getenv("APP_PORT")
	if port == "" {
		port = "8093"
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
	cfg, err := config.LoadPayoutService()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if os.Getenv("APP_PORT") == "" {
		cfg.App.Port = "8093"
	}
	if os.Getenv("GRPC_PORT") == "" {
		cfg.GRPCPort = "9093"
	}
	log := logger.New(cfg.Logger.Pkg())
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTracing, err := tracing.Setup(ctx, tracing.Config{
		ServiceName: "payout-service",
		Endpoint:    cfg.Tracing.OTLPEndpoint,
		SampleRatio: cfg.Tracing.SampleRatio,
		Insecure:    cfg.Tracing.Insecure,
	})
	if err != nil {
		log.Error("tracing: setup failed, continuing without a tracer provider", "error", err)
		shutdownTracing = func(context.Context) error { return nil }
	}
	db, err := database.New(ctx, cfg.Postgres.Pkg())
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	cfg.Redis.DB = 0
	var redisCache *cache.Cache
	var redisClient *redis.Client
	if cfg.Redis.Enabled {
		redisCache, err = cache.New(ctx, cfg.Redis.Pkg())
		if err != nil {
			_ = db.Close()
			return fmt.Errorf("connect redis db 0: %w", err)
		}
		redisClient = redisCache.Redis()
	}
	ledgerConn, err := grpcx.Dial(ctx, cfg.LedgerGRPCAddr, cfg.InternalGRPCToken)
	if err != nil {
		if redisCache != nil {
			_ = redisCache.Close()
		}
		_ = db.Close()
		return fmt.Errorf("connect ledger-service: %w", err)
	}
	registry := vendorgw.NewRegistry()
	if cfg.Vendor.MockvendorEnabled {
		registry.AddPayout(mockvendor.NewPayoutProvider(mockvendor.VendorName))
		log.Warn("vendorgw: mockvendor enabled — test-only vendor")
	}
	if cfg.Vendor.Mockvendor2Enabled {
		registry.AddPayout(mockvendor.NewPayoutProvider("mockvendor2"))
		log.Warn("vendorgw: mockvendor2 enabled — test-only second vendor for failover demos")
	}
	var breaker vendorgw.Breaker = vendorgw.NewHealthTracker(cfg.Breaker.FailureThreshold, cfg.Breaker.Cooldown, log)
	if cfg.Breaker.Distributed && redisClient != nil {
		breaker = vendorgw.NewDistributedBreaker(redisClient, "payout", cfg.Breaker.FailureThreshold, cfg.Breaker.Cooldown, 0, log)
		log.Info("vendorgw: distributed breaker enabled", "namespace", "payout")
	}

	// fraud client screens payouts pre-hold (docs/plan/37 Task T5).
	// FRAUD_GRPC_ADDR unset (dev/test defaults) => nil client => no screening.
	var fraudClient *fraudcheck.Client
	var fraudConn *grpc.ClientConn
	if cfg.FraudGRPCAddr != "" {
		fraudConn, err = grpcx.DialLazy(ctx, cfg.FraudGRPCAddr, cfg.InternalGRPCToken)
		if err != nil {
			_ = ledgerConn.Close()
			if redisCache != nil {
				_ = redisCache.Close()
			}
			_ = db.Close()
			return fmt.Errorf("create fraud-service client: %w", err)
		}
		defer func() { _ = fraudConn.Close() }()
		fraudClient = fraudcheck.New(fraudv1.NewFraudServiceClient(fraudConn), "payout")
	}

	module := payout.NewModule(db, ledgerclient.New(ledgerConn), registry, redisClient, log, fraudClient, breaker)
	module.StartWorkers(ctx)
	grpcServer := grpcx.NewServer(log, cfg.InternalGRPCToken)
	module.RegisterGRPC(grpcServer)
	listener, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		module.StopWorkers()
		_ = ledgerConn.Close()
		if redisCache != nil {
			_ = redisCache.Close()
		}
		_ = db.Close()
		return err
	}
	httpServer := &http.Server{Addr: ":" + cfg.App.Port, Handler: adminRouter(cfg, module, log), ReadTimeout: cfg.App.ReadTimeout, WriteTimeout: cfg.App.WriteTimeout, IdleTimeout: cfg.App.IdleTimeout, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 1 << 20}
	errCh := make(chan error, 2)
	go serveGRPC(grpcServer, listener, errCh)
	go serveHTTP(httpServer, errCh)
	log.Info("payout-service started", "grpc", listener.Addr(), "admin_http", httpServer.Addr, "redis_db", 0)
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
	module.StopWorkers()
	_ = ledgerConn.Close()
	if redisCache != nil {
		_ = redisCache.Close()
	}
	_ = db.Close()
	_ = shutdownTracing(context.Background())
	return serveErr
}
func serveHTTP(s *http.Server, ch chan<- error) {
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		ch <- err
	}
}
func serveGRPC(s *grpc.Server, l net.Listener, ch chan<- error) {
	if err := s.Serve(l); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		ch <- err
	}
}
func gracefulStopGRPC(s *grpc.Server, timeout time.Duration) {
	done := make(chan struct{})
	go func() { s.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		s.Stop()
	}
}
