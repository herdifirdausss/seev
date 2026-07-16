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

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/fraud"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/grpcx"
	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/messaging"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe fraud-service liveness endpoint")
	flag.Parse()
	if *healthcheck {
		if err := probeHealth(os.Getenv); err != nil {
			slog.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(context.Background()); err != nil {
		slog.Error("fraud-service stopped", "error", err)
		os.Exit(1)
	}
}

func probeHealth(getenv func(string) string) error {
	port := getenv("APP_PORT")
	if port == "" {
		port = "8094"
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
	cfg, err := config.LoadFraudService()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if os.Getenv("APP_PORT") == "" {
		cfg.App.Port = "8094"
	}
	if os.Getenv("GRPC_PORT") == "" {
		cfg.GRPCPort = "9094"
	}
	log := logger.New(cfg.Logger.Pkg())
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	db, err := database.New(ctx, cfg.Postgres.Pkg())
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	cfg.Redis.Enabled = true
	cfg.Redis.DB = 1
	redisCache, err := cache.New(ctx, cfg.Redis.Pkg())
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("connect redis db 1: %w", err)
	}
	broker, err := messaging.New(ctx, cfg.RabbitMQ.Broker())
	if err != nil {
		_ = redisCache.Close()
		_ = db.Close()
		return fmt.Errorf("connect rabbitmq: %w", err)
	}
	store := fraud.NewRedisVelocityStore(redisCache.Redis())
	module := fraud.NewModule(db, store, broker, fraud.Config{
		Mode:               cfg.Fraud.ScreeningMode,
		AmountThreshold:    decimal.NewFromInt(cfg.Fraud.ScreeningAmountThreshold),
		VelocityMaxPerHour: cfg.Fraud.ScreeningVelocityMaxPerHour,
	}, log)
	if err := module.Start(ctx); err != nil {
		_ = broker.Close()
		_ = redisCache.Close()
		_ = db.Close()
		return err
	}

	grpcServer := grpcx.NewServer(log, cfg.InternalGRPCToken)
	module.RegisterGRPC(grpcServer)
	listener, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		module.Stop()
		_ = broker.Close()
		_ = redisCache.Close()
		_ = db.Close()
		return err
	}
	httpServer := &http.Server{
		Addr: ":" + cfg.App.Port, Handler: adminRouter(cfg, module, log),
		ReadTimeout: cfg.App.ReadTimeout, WriteTimeout: cfg.App.WriteTimeout,
		IdleTimeout: cfg.App.IdleTimeout, ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	errCh := make(chan error, 2)
	go serveGRPC(grpcServer, listener, errCh)
	go serveHTTP(httpServer, errCh)
	log.Info("fraud-service started", "grpc", listener.Addr(), "admin_http", httpServer.Addr, "redis_db", 1)
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
	module.Stop()
	_ = broker.Close()
	_ = redisCache.Close()
	_ = db.Close()
	return serveErr
}

func serveHTTP(server *http.Server, errorsOut chan<- error) {
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errorsOut <- err
	}
}

func serveGRPC(server *grpc.Server, listener net.Listener, errorsOut chan<- error) {
	if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		errorsOut <- err
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
