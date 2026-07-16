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

	"github.com/herdifirdausss/seev/internal/auth"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/kycvendor/mockkyc"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/grpcx"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/logger"
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

func probeHealth(getenv func(string) string) error {
	port := getenv("INTERNAL_APP_PORT")
	if port == "" {
		port = "8083"
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
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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
	ledgerConn, err := grpcx.Dial(ctx, cfg.LedgerGRPCAddr, cfg.InternalGRPCToken)
	if err != nil {
		closeAuthDependencies(log, nil, redisCache, db)
		return fmt.Errorf("connect ledger-service: %w", err)
	}
	module := auth.NewModule(db, ledgerclient.New(ledgerConn), auth.Config{
		JWTSecret: cfg.JWT.Secret, JWTIssuer: cfg.JWT.Issuer,
		AccessExpiry: cfg.JWT.AccessExpiry, RefreshExpiry: cfg.JWT.RefreshExpiry,
		DefaultCurrency: cfg.Auth.DefaultCurrency,
	}, log, mockkyc.New())
	if err := module.EnsureBootstrapAdmin(ctx, cfg.Auth.BootstrapAdminEmail, cfg.Auth.BootstrapAdminPassword); err != nil {
		closeAuthDependencies(log, ledgerConn.Close, redisCache, db)
		return fmt.Errorf("ensure bootstrap admin: %w", err)
	}

	publicServer := newHTTPServer(cfg.App, ":"+cfg.App.Port, publicRouter(cfg, module, redisCache, log))
	internalServer := newHTTPServer(cfg.App, cfg.App.InternalBindAddr+":"+cfg.App.InternalPort, internalRouter(cfg, module))
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
	if err := publicServer.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = err
	}
	if err := internalServer.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = err
	}
	closeAuthDependencies(log, ledgerConn.Close, redisCache, db)
	return serveErr
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

func closeAuthDependencies(log *slog.Logger, closeLedger func() error, redisCache *cache.Cache, db *database.DBSQL) {
	if closeLedger != nil {
		if err := closeLedger(); err != nil {
			log.Error("close ledger grpc", "error", err)
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
}
