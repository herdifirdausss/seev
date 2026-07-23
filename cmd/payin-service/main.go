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
	"github.com/herdifirdausss/seev/internal/payin"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/internal/vendorgw/mockvendor"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/grpcx"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/tlsx"
	"github.com/herdifirdausss/seev/pkg/tracing"
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
	certDir := getenv("TLS_CERT_DIR")
	if certDir == "" {
		certDir = "deploy/certs"
	}
	certSrc, err := tlsx.LoadFromDir(certDir, "dev-operator", slog.Default())
	if err != nil {
		return fmt.Errorf("load healthcheck TLS identity: %w", err)
	}
	defer certSrc.Stop()
	client := tlsx.HTTPClient(certSrc, tlsx.IdentityPayin, 3*time.Second)
	res, err := client.Get("https://127.0.0.1:" + port + "/health")
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
	// docs/roadmap/archive/49 K3/K5: load this process's own identity + the shared CA
	// before anything else.
	certSrc, err := tlsx.LoadFromDir(cfg.TLSCertDir, "payin", log)
	if err != nil {
		return fmt.Errorf("load TLS certificates: %w", err)
	}
	defer certSrc.Stop()
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTracing, err := tracing.Setup(ctx, tracing.Config{
		ServiceName: "payin-service",
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
	// Redis is entirely optional for payin-service today — its only
	// consumer is an opt-in distributed breaker (docs/roadmap/archive/45 Task T2,
	// BREAKER_DISTRIBUTED, default false). Same nil-means-disabled
	// convention as payout/ledger-service's own Redis wiring; DB 0 is safe
	// to share with payout's own breaker keys since every key is namespaced
	// ("breaker:payin:..." vs "breaker:payout:...").
	cfg.Redis.DB = 0
	var redisCache *cache.Cache
	var redisClient *redis.Client
	// Payin only consumes Redis for the optional distributed breaker. Keep the
	// default local breaker self-contained so a container does not try to dial
	// the host-oriented Redis default (localhost:6380) during startup.
	if cfg.Breaker.Distributed && cfg.Redis.Enabled {
		redisCache, err = cache.New(ctx, cfg.Redis.Pkg())
		if err != nil {
			_ = db.Close()
			return fmt.Errorf("connect redis db 0: %w", err)
		}
		redisClient = redisCache.Redis()
	}
	ledgerConn, err := grpcx.Dial(ctx, cfg.LedgerGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityLedger))
	if err != nil {
		if redisCache != nil {
			_ = redisCache.Close()
		}
		_ = db.Close()
		return fmt.Errorf("connect ledger-service: %w", err)
	}
	registry := vendorgw.NewRegistry()
	if cfg.Vendor.MockvendorEnabled {
		registry.AddPayin(mockvendor.New(mockvendor.VendorName, cfg.Vendor.MockvendorSecret))
		log.Warn("vendorgw: mockvendor enabled — test-only vendor")
	}
	if cfg.Vendor.Mockvendor2Enabled {
		registry.AddPayin(mockvendor.New("mockvendor2", cfg.Vendor.Mockvendor2Secret))
		log.Warn("vendorgw: mockvendor2 enabled — test-only second vendor for failover demos")
	}
	var breaker vendorgw.Breaker = vendorgw.NewHealthTracker(cfg.Breaker.FailureThreshold, cfg.Breaker.Cooldown, log)
	if cfg.Breaker.Distributed && redisClient != nil {
		breaker = vendorgw.NewDistributedBreaker(redisClient, "payin", cfg.Breaker.FailureThreshold, cfg.Breaker.Cooldown, 0, log)
		log.Info("vendorgw: distributed breaker enabled", "namespace", "payin")
	}

	// fraud client screens deposits pre-posting (docs/roadmap/archive/37 Task T4).
	// FRAUD_GRPC_ADDR unset (dev/test defaults) => nil client => no screening.
	var fraudClient *fraudcheck.Client
	var fraudConn *grpc.ClientConn
	if cfg.FraudGRPCAddr != "" {
		fraudConn, err = grpcx.DialLazy(ctx, cfg.FraudGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityFraud))
		if err != nil {
			_ = ledgerConn.Close()
			if redisCache != nil {
				_ = redisCache.Close()
			}
			_ = db.Close()
			return fmt.Errorf("create fraud-service client: %w", err)
		}
		defer func() { _ = fraudConn.Close() }()
		fraudClient = fraudcheck.New(fraudv1.NewFraudServiceClient(fraudConn), "payin")
	}

	module := payin.NewModule(db, ledgerclient.New(ledgerConn), registry, cfg.Vendor.TopupIntentTTL, log, fraudClient, breaker)
	// docs/roadmap/archive/49 K4: gateway calls payin's gRPC surface for user-facing
	// topup flows; assurance-service (TM-09 — added after K4 was written,
	// see docs/security/threat-model.md §4) reads it for cross-service
	// correlation. Verified live: no other caller exists.
	grpcServer, err := grpcx.NewServer(log, cfg.InternalGRPCToken, tlsx.ServerConfig(certSrc, []string{
		tlsx.IdentityGateway, tlsx.IdentityAssurance,
	}))
	if err != nil {
		_ = ledgerConn.Close()
		if redisCache != nil {
			_ = redisCache.Close()
		}
		_ = db.Close()
		return fmt.Errorf("create grpc server: %w", err)
	}
	module.RegisterGRPC(grpcServer)
	grpcListener, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		_ = ledgerConn.Close()
		if redisCache != nil {
			_ = redisCache.Close()
		}
		_ = db.Close()
		return fmt.Errorf("listen grpc: %w", err)
	}
	// docs/roadmap/archive/49 K6: payin's admin listener is internal-only mTLS.
	httpServer := &http.Server{Addr: ":" + cfg.App.Port, Handler: adminRouter(cfg, module, log), ReadTimeout: cfg.App.ReadTimeout, WriteTimeout: cfg.App.WriteTimeout, IdleTimeout: cfg.App.IdleTimeout, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 1 << 20, TLSConfig: tlsx.ServerConfig(certSrc, []string{
		tlsx.IdentityDevOperator, tlsx.IdentityPrometheus, tlsx.IdentityAdminBFF,
	})}
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
	if redisCache != nil {
		if err := redisCache.Close(); err != nil {
			log.Error("close redis", "error", err)
		}
	}
	if err := db.Close(); err != nil {
		log.Error("close postgres", "error", err)
	}
	if err := shutdownTracing(context.Background()); err != nil {
		log.Error("close tracing", "error", err)
	}
	return serveErr
}

func serveHTTP(server *http.Server, errCh chan<- error) {
	var err error
	if server.TLSConfig != nil {
		err = server.ListenAndServeTLS("", "")
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
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
