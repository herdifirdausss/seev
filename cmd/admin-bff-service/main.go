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

	"github.com/herdifirdausss/seev/internal/adminbff"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/tracing"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe admin-bff-service liveness endpoint")
	flag.Parse()
	if *healthcheck {
		if err := probeHealth(os.Getenv); err != nil {
			slog.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(context.Background()); err != nil {
		slog.Error("admin-bff-service stopped", "error", err)
		os.Exit(1)
	}
}

func probeHealth(getenv func(string) string) error {
	port := getenv("APP_PORT")
	if port == "" {
		port = "8095"
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
	cfg, err := config.LoadAdminBFFService()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if os.Getenv("APP_PORT") == "" {
		cfg.App.Port = "8095"
	}
	log := logger.New(cfg.Logger.Pkg())
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTracing, err := tracing.Setup(ctx, tracing.Config{
		ServiceName: "admin-bff-service", Endpoint: cfg.Tracing.OTLPEndpoint,
		SampleRatio: cfg.Tracing.SampleRatio, Insecure: cfg.Tracing.Insecure,
	})
	if err != nil {
		log.Error("tracing: setup failed, continuing without a tracer provider", "error", err)
		shutdownTracing = func(context.Context) error { return nil }
	}

	db, err := database.New(ctx, cfg.Postgres.Pkg())
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer db.Close()
	defer func() {
		if shutdownErr := shutdownTracing(context.Background()); shutdownErr != nil {
			log.Error("tracing: shutdown failed", "error", shutdownErr)
		}
	}()

	module := adminbff.NewModule(db, cfg.AdminBFF, log)
	if err := module.Start(); err != nil {
		return fmt.Errorf("start admin-bff jobs: %w", err)
	}
	defer module.Stop()
	server := &http.Server{
		Addr: ":" + cfg.App.Port, Handler: adminRouter(cfg, module, log),
		ReadTimeout: cfg.App.ReadTimeout, WriteTimeout: cfg.App.WriteTimeout,
		IdleTimeout: cfg.App.IdleTimeout, ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	errCh := make(chan error, 1)
	go func() {
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()
	log.Info("admin-bff-service started", "http", server.Addr)
	select {
	case <-ctx.Done():
	case err = <-errCh:
		cancel()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer shutdownCancel()
	if shutdownErr := server.Shutdown(shutdownCtx); err == nil {
		err = shutdownErr
	}
	return err
}
