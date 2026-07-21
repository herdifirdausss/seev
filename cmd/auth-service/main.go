package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"google.golang.org/grpc"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	fraudv1 "github.com/herdifirdausss/seev/gen/fraud/v1"
	"github.com/herdifirdausss/seev/internal/auth"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/kycvendor/mockkyc"
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
	healthcheck := flag.Bool("healthcheck", false, "probe the auth-service liveness endpoint")
	flag.Parse()
	if *healthcheck {
		if err := probeHealth(os.Getenv); err != nil {
			slog.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(context.Background()); err != nil {
		slog.Error("auth-service stopped", "error", err)
		os.Exit(1)
	}
}

// probeHealth dials the INTERNAL :8083 listener, which is mTLS since
// docs/plan/49 K6 flips it — auth's PUBLIC :8082 has no separate
// healthcheck path and stays plain (anti-scope: edge-public exception).
func probeHealth(getenv func(string) string) error {
	port := getenv("INTERNAL_APP_PORT")
	if port == "" {
		port = "8083"
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
	client := tlsx.HTTPClient(certSrc, tlsx.IdentityAuth, 3*time.Second)
	response, err := client.Get("https://127.0.0.1:" + port + "/health")
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
	cfg, err := config.LoadAuthService()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if os.Getenv("APP_PORT") == "" {
		cfg.App.Port = "8082"
	}
	if os.Getenv("INTERNAL_APP_PORT") == "" {
		cfg.App.InternalPort = "8083"
	}
	log := logger.New(cfg.Logger.Pkg())
	// docs/plan/49 K3/K5: load this process's own identity + the shared CA
	// before anything else.
	certSrc, err := tlsx.LoadFromDir(cfg.TLSCertDir, "auth", log)
	if err != nil {
		return fmt.Errorf("load TLS certificates: %w", err)
	}
	defer certSrc.Stop()
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTracing, err := tracing.Setup(ctx, tracing.Config{
		ServiceName: "auth-service",
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
	var redisCache *cache.Cache
	if cfg.Redis.Enabled {
		cfg.Redis.DB = 0
		redisCache, err = cache.New(ctx, cfg.Redis.Pkg())
		if err != nil {
			_ = db.Close()
			return fmt.Errorf("connect redis: %w", err)
		}
	}
	ledgerConn, err := grpcx.Dial(ctx, cfg.LedgerGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityLedger))
	if err != nil {
		closeAuthDependencies(log, nil, nil, redisCache, db, shutdownTracing)
		return fmt.Errorf("connect ledger-service: %w", err)
	}
	var fraudConn *grpc.ClientConn
	var closeFraud func() error
	if cfg.FraudGRPCAddr != "" {
		conn, dialErr := grpcx.DialLazy(ctx, cfg.FraudGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityFraud))
		if dialErr != nil {
			closeAuthDependencies(log, ledgerConn.Close, nil, redisCache, db, shutdownTracing)
			return fmt.Errorf("connect fraud-service: %w", dialErr)
		}
		fraudConn = conn
		closeFraud = fraudConn.Close
	}
	module := auth.NewModule(db, ledgerclient.New(ledgerConn), auth.Config{
		JWTSecret: cfg.JWT.Secret, JWTIssuer: cfg.JWT.Issuer,
		AccessExpiry: cfg.JWT.AccessExpiry, RefreshExpiry: cfg.JWT.RefreshExpiry,
		DefaultCurrency: cfg.Auth.DefaultCurrency,
	}, log, mockkyc.New())
	if fraudConn != nil {
		module.SetSanctionsChecker(fraudcheck.New(fraudv1.NewFraudServiceClient(fraudConn), "auth"))
	}
	if kek := os.Getenv("KYC_DOC_KEK"); kek != "" {
		module.SetDocumentKEK([]byte(kek))
	}
	if err := module.EnsureBootstrapAdmin(ctx, cfg.Auth.BootstrapAdminEmail, cfg.Auth.BootstrapAdminPassword); err != nil {
		closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
		return fmt.Errorf("ensure bootstrap admin: %w", err)
	}
	if err := module.EnsureBootstrapOperator(ctx, cfg.Auth.BootstrapMakerEmail, cfg.Auth.BootstrapMakerPassword, "admin_maker"); err != nil {
		closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
		return fmt.Errorf("ensure bootstrap maker: %w", err)
	}
	if err := module.EnsureBootstrapOperator(ctx, cfg.Auth.BootstrapCheckerEmail, cfg.Auth.BootstrapCheckerPassword, "admin_checker"); err != nil {
		closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
		return fmt.Errorf("ensure bootstrap checker: %w", err)
	}
	retryJob := module.NewKYCApplyRetryJob(redisClientClient(redisCache), log)
	if err := retryJob.Start(ctx); err != nil {
		closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
		return fmt.Errorf("start kyc apply retry worker: %w", err)
	}

	// docs/plan/49 K6: auth's public :8082 stays plain (anti-scope edge
	// exception); only the internal :8083 listener flips to mTLS.
	publicServer := newHTTPServer(cfg.App, ":"+cfg.App.Port, publicRouter(cfg, module, redisCache, log), nil)
	internalServer := newHTTPServer(cfg.App, cfg.App.InternalBindAddr+":"+cfg.App.InternalPort, internalRouter(cfg, module), tlsx.ServerConfig(certSrc, []string{
		tlsx.IdentityDevOperator, tlsx.IdentityPrometheus, tlsx.IdentityAdminBFF,
	}))
	errCh := make(chan error, 2)
	go serveHTTP(publicServer, errCh)
	go serveHTTP(internalServer, errCh)
	log.Info("auth-service started", "http", publicServer.Addr, "internal_http", internalServer.Addr)

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errCh:
		cancel()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer shutdownCancel()
	retryJob.Stop()
	if err := publicServer.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = err
	}
	if err := internalServer.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = err
	}
	closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
	return serveErr
}

func redisClientClient(c *cache.Cache) *redis.Client {
	if c == nil {
		return nil
	}
	return c.Client()
}

func newHTTPServer(cfg config.AppConfig, addr string, handler http.Handler, tlsConfig *tls.Config) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout,
		IdleTimeout: cfg.IdleTimeout, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 1 << 20, TLSConfig: tlsConfig}
}

func serveHTTP(server *http.Server, errCh chan<- error) {
	var err error
	if server.TLSConfig != nil {
		err = server.ListenAndServeTLS("", "")
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- fmt.Errorf("http %s: %w", server.Addr, err)
	}
}

func closeAuthDependencies(log *slog.Logger, closeLedger func() error, closeFraud func() error, redisCache *cache.Cache, db *database.DBSQL, shutdownTracing func(context.Context) error) {
	if closeLedger != nil {
		if err := closeLedger(); err != nil {
			log.Error("close ledger grpc", "error", err)
		}
	}
	if closeFraud != nil {
		if err := closeFraud(); err != nil {
			log.Error("close fraud grpc", "error", err)
		}
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
}
