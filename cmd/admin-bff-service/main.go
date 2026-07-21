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
	"github.com/herdifirdausss/seev/pkg/tlsx"
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
	certDir := getenv("TLS_CERT_DIR")
	if certDir == "" {
		certDir = "deploy/certs"
	}
	certSrc, err := tlsx.LoadFromDir(certDir, "dev-operator", slog.Default())
	if err != nil {
		return fmt.Errorf("load healthcheck TLS identity: %w", err)
	}
	defer certSrc.Stop()
	response, err := tlsx.HTTPClient(certSrc, tlsx.IdentityAdminBFF, 3*time.Second).Get("https://127.0.0.1:" + port + "/health")
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
	// docs/plan/49 K6: admin-bff was deliberately excluded from T2's gRPC-
	// only cert mount ("itu lingkup T3 HTTP bukan T2 gRPC") — this is
	// where it gains its own identity, both to serve its own listener and
	// to dial every downstream service it proxies for except auth's
	// public login endpoint.
	certSrc, err := tlsx.LoadFromDir(cfg.TLSCertDir, "admin-bff", log)
	if err != nil {
		return fmt.Errorf("load TLS certificates: %w", err)
	}
	defer certSrc.Stop()
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

	module := adminbff.NewModule(db, cfg.AdminBFF, log, certSrc)
	if err := module.Start(); err != nil {
		return fmt.Errorf("start admin-bff jobs: %w", err)
	}
	defer module.Stop()
	// docs/plan/49 K6: admin-bff's console listener requires mTLS like
	// every other internal surface — an operator's browser needs a
	// dev-operator client certificate loaded, same as this harness's own
	// curl_internal (docs/plan/49 §T3 constraint 5's own justification).
	server := &http.Server{
		Addr: ":" + cfg.App.Port, Handler: adminRouter(cfg, module, log),
		ReadTimeout: cfg.App.ReadTimeout, WriteTimeout: cfg.App.WriteTimeout,
		IdleTimeout: cfg.App.IdleTimeout, ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes: 1 << 20,
		TLSConfig: tlsx.ServerConfig(certSrc, []string{
			tlsx.IdentityDevOperator, tlsx.IdentityPrometheus,
		}),
	}
	errCh := make(chan error, 1)
	go func() {
		if serveErr := server.ListenAndServeTLS("", ""); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
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
