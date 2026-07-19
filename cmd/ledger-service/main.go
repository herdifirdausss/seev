package main

import (
	"context"
	"encoding/json"
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

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc"

	fraudv1 "github.com/herdifirdausss/seev/gen/fraud/v1"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/ledger"
	"github.com/herdifirdausss/seev/internal/policy"
	"github.com/herdifirdausss/seev/pkg/alerting"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/grpcx"
	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/messaging"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/tracing"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the ledger-service liveness endpoint")
	flag.Parse()
	if *healthcheck {
		if err := probeHealth(os.Getenv); err != nil {
			slog.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(context.Background()); err != nil {
		slog.Error("ledger-service stopped", "error", err)
		os.Exit(1)
	}
}

func probeHealth(getenv func(string) string) error {
	port := getenv("APP_PORT")
	if port == "" {
		port = "8090"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://127.0.0.1:" + port + "/health")
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
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	// The shared config keeps monolith-compatible defaults; this binary owns
	// the ledger-specific ports when no explicit override is provided.
	if os.Getenv("APP_PORT") == "" {
		cfg.App.Port = "8090"
	}
	if os.Getenv("INTERNAL_APP_PORT") == "" {
		cfg.App.InternalPort = "8091"
	}
	log := logger.New(cfg.Logger.Pkg())
	for _, warning := range cfg.Warnings() {
		log.Warn("config: " + warning)
	}

	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTracing, err := tracing.Setup(ctx, tracing.Config{
		ServiceName: "ledger-service",
		Endpoint:    cfg.Tracing.OTLPEndpoint,
		SampleRatio: cfg.Tracing.SampleRatio,
		Insecure:    cfg.Tracing.Insecure,
	})
	if err != nil {
		log.Error("tracing: setup failed, continuing without exporter", "error", err)
		shutdownTracing = func(context.Context) error { return nil }
	}

	db, err := database.New(ctx, cfg.Postgres.Pkg())
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	var redisCache *cache.Cache
	var redisClient *redis.Client
	if cfg.Redis.Enabled {
		redisCache, err = cache.New(ctx, cfg.Redis.Pkg())
		if err != nil {
			_ = db.Close()
			return fmt.Errorf("connect redis: %w", err)
		}
		redisClient = redisCache.Redis()
	}
	mq, err := messaging.New(ctx, cfg.RabbitMQ.Broker())
	if err != nil {
		if redisCache != nil {
			_ = redisCache.Close()
		}
		_ = db.Close()
		return fmt.Errorf("connect rabbitmq: %w", err)
	}

	policyLoc, locationErr := time.LoadLocation("Asia/Jakarta")
	if locationErr != nil {
		policyLoc = time.UTC
	}
	var counter cache.Counter = cache.NewMemoryCounter()
	if redisClient != nil {
		// docs/plan/45 Task T3/K4: fails over to an in-memory counter at
		// runtime if Redis becomes unreachable, recovering automatically —
		// a strictly stronger degradation than the prior fail-open-with-no-
		// enforcement gap, never a substitute for real cross-replica
		// enforcement.
		counter = cache.NewFailoverCounter(redisClient, log)
	}
	var policyOpts []policy.Option
	if cfg.Worker.AlertWebhookURL != "" {
		policyOpts = append(policyOpts, policy.WithAlertFunc(alerting.NewWebhookAlerter(cfg.Worker.AlertWebhookURL, nil)))
	}
	if cfg.Ledger.PolicyCacheTTL > 0 {
		policyOpts = append(policyOpts, policy.WithCacheTTL(cfg.Ledger.PolicyCacheTTL))
	}
	policyRepo := policy.NewRepository(db)
	policyEngine := policy.New(policyRepo, counter, policyLoc, log, policyOpts...)
	policyHandler := policy.NewHandler(policyRepo)
	// Screening moved out of the posting transaction (docs/plan/37) — the
	// fraud client is passed into the PUBLIC router (transport.NewRouterWithFraud
	// inside ledger.NewModule below), called BEFORE any DB work, not into
	// the posting pipeline itself.
	var fraudClient *fraudcheck.Client
	var fraudConn *grpc.ClientConn
	if cfg.FraudGRPCAddr != "" {
		fraudConn, err = grpcx.DialLazy(ctx, cfg.FraudGRPCAddr, cfg.InternalGRPCToken)
		if err != nil {
			closeDependencies(log, nil, mq, redisCache, db, shutdownTracing)
			return fmt.Errorf("create fraud-service client: %w", err)
		}
		defer func() { _ = fraudConn.Close() }()
		fraudClient = fraudcheck.New(fraudv1.NewFraudServiceClient(fraudConn), "ledger")
	}

	module := ledger.NewModule(db, mq, redisClient, ledger.WorkerConfig{
		Enabled: cfg.Worker.Enabled, OutboxPollInterval: cfg.Worker.OutboxPollInterval,
		OutboxBatchSize: cfg.Worker.OutboxBatchSize, AlertWebhookURL: cfg.Worker.AlertWebhookURL,
	}, log, decimal.NewFromInt(cfg.Ledger.MaxAmountPerTx), policyEngine, fraudClient, cfg.Ledger.FeeQuoteTTL)
	if err := module.LoadCurrencies(ctx); err != nil {
		closeDependencies(log, module, mq, redisCache, db, shutdownTracing)
		return err
	}
	module.StartWorkers(ctx)

	grpcServer := grpcx.NewServer(log, cfg.InternalGRPCToken)
	module.RegisterGRPC(grpcServer)
	grpcListener, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		closeDependencies(log, module, mq, redisCache, db, shutdownTracing)
		return fmt.Errorf("listen grpc: %w", err)
	}

	publicServer := newHTTPServer(cfg.App, ":"+cfg.App.Port, publicRouter(cfg, module, db, redisCache, mq, log))
	internalServer := newHTTPServer(cfg.App, cfg.App.InternalBindAddr+":"+cfg.App.InternalPort, internalRouter(cfg, module, policyHandler, log))
	errCh := make(chan error, 3)
	go serveGRPC(grpcServer, grpcListener, errCh)
	go serveHTTP(publicServer, errCh)
	go serveHTTP(internalServer, errCh)
	log.Info("ledger-service started", "grpc", grpcListener.Addr(), "http", publicServer.Addr, "internal_http", internalServer.Addr)

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errCh:
		cancel()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer shutdownCancel()
	if err := publicServer.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = err
	}
	if err := internalServer.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = err
	}
	gracefulStopGRPC(grpcServer, cfg.App.ShutdownTimeout)
	closeDependencies(log, module, mq, redisCache, db, shutdownTracing)
	return serveErr
}

func publicRouter(cfg *config.Config, module *ledger.Module, db *database.DBSQL, redisCache *cache.Cache, mq *messaging.RabbitMQ, log *slog.Logger) http.Handler {
	root := http.NewServeMux()
	root.HandleFunc("GET /health", live)
	root.Handle("GET /ready", ready(db, redisCache, mq))
	authed := middleware.Chain(middleware.WithAuth(cfg.JWT.Secret, cfg.JWT.Issuer), middleware.RequireJSON())
	api := http.NewServeMux()
	api.Handle("/api/v1/ledger/", authed(http.StripPrefix("/api/v1/ledger", module.Router())))
	root.Handle("/", middleware.Chain(
		middleware.WithRequestID(), middleware.WithRoutePattern(api), middleware.WithTracing(log), middleware.WithHTTPMetrics(), middleware.WithLogger(log), middleware.WithRecovery(),
		middleware.WithSecurityHeaders(middleware.DefaultSecurityHeadersConfig()), middleware.WithTimeout(30*time.Second),
	)(api))
	return root
}

func internalRouter(cfg *config.Config, module *ledger.Module, policyHandler *policy.Handler, log *slog.Logger) http.Handler {
	root := http.NewServeMux()
	root.Handle("GET /metrics", promhttp.Handler())
	authed := middleware.Chain(middleware.WithAuth(cfg.JWT.Secret, cfg.JWT.Issuer), middleware.RequireJSON())
	api := http.NewServeMux()
	api.Handle("/api/v1/ledger/", authed(http.StripPrefix("/api/v1/ledger", module.InternalRouter())))
	api.Handle("/api/v1/admin/ledger/", authed(http.StripPrefix("/api/v1", module.InternalRouter())))
	api.Handle("/api/v1/admin/policy/", authed(http.StripPrefix("/api/v1", policyHandler.Mux())))
	root.Handle("/", middleware.Chain(
		middleware.WithRequestID(), middleware.WithRoutePattern(api), middleware.WithTracing(log), middleware.WithHTTPMetrics(), middleware.WithLogger(log), middleware.WithRecovery(),
		middleware.WithSecurityHeaders(middleware.DefaultSecurityHeadersConfig()), middleware.WithTimeout(30*time.Second),
	)(api))
	return root
}

func live(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

type healthDB interface{ HealthCheck(context.Context) error }
type healthCache interface{ HealthCheck(context.Context) error }
type healthMQ interface{ HealthCheck() error }

func ready(db healthDB, redisCache healthCache, mq healthMQ) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		components := map[string]string{}
		healthy := true
		if err := db.HealthCheck(r.Context()); err != nil {
			components["postgres"], healthy = err.Error(), false
		} else {
			components["postgres"] = "ok"
		}
		if redisCache == nil {
			components["redis"] = "disabled"
		} else if err := redisCache.HealthCheck(r.Context()); err != nil {
			components["redis"], healthy = err.Error(), false
		} else {
			components["redis"] = "ok"
		}
		if err := mq.HealthCheck(); err != nil {
			components["rabbitmq"], healthy = err.Error(), false
		} else {
			components["rabbitmq"] = "ok"
		}
		statusCode := http.StatusOK
		if !healthy {
			statusCode = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": map[bool]string{true: "ok", false: "degraded"}[healthy], "components": components})
	}
}

func newHTTPServer(cfg config.AppConfig, addr string, handler http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout,
		IdleTimeout: cfg.IdleTimeout, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 1 << 20}
}

func serveHTTP(server *http.Server, errCh chan<- error) {
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- fmt.Errorf("http %s: %w", server.Addr, err)
	}
}

func serveGRPC(server *grpc.Server, listener net.Listener, errCh chan<- error) {
	if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		errCh <- fmt.Errorf("grpc %s: %w", listener.Addr(), err)
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

func closeDependencies(log *slog.Logger, module *ledger.Module, mq *messaging.RabbitMQ, redisCache *cache.Cache, db *database.DBSQL, shutdownTracing func(context.Context) error) {
	if module != nil {
		module.StopWorkers()
	}
	if err := mq.Close(); err != nil {
		log.Error("close rabbitmq", "error", err)
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
		log.Error("shutdown tracing", "error", err)
	}
}
