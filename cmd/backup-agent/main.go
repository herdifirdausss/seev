// backup-agent is docs/plan/50 T2's operational agent: it owns the
// weekly-full/daily-differential pgBackRest schedule and exposes
// mTLS-protected /health, /ready, /metrics on an internal-only listener
// (K13). It is not a domain service — no public listener, no gRPC.
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

	"github.com/herdifirdausss/seev/internal/backupagent"
	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/tlsx"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe backup-agent liveness endpoint")
	flag.Parse()

	if *healthcheck {
		if err := probeHealth(os.Getenv); err != nil {
			slog.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}

	if flag.NArg() > 0 && flag.Arg(0) == "status" {
		if err := runStatus(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "backup-agent status:", err)
			os.Exit(1)
		}
		return
	}

	if flag.NArg() > 0 && (flag.Arg(0) == "backup-full" || flag.Arg(0) == "backup-diff") {
		backupType := "full"
		if flag.Arg(0) == "backup-diff" {
			backupType = "diff"
		}
		if err := runBackupNow(context.Background(), backupType); err != nil {
			fmt.Fprintln(os.Stderr, "backup-agent "+flag.Arg(0)+":", err)
			os.Exit(1)
		}
		return
	}

	if err := run(context.Background()); err != nil {
		slog.Error("backup-agent stopped", "error", err)
		os.Exit(1)
	}
}

func probeHealth(getenv func(string) string) error {
	port := getenv("APP_PORT")
	if port == "" {
		port = "8097"
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
	response, err := tlsx.HTTPClient(certSrc, tlsx.IdentityBackupAgent, 3*time.Second).Get("https://127.0.0.1:" + port + "/health")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}

// runStatus is a one-shot, in-process status read (no HTTP round trip —
// it runs inside the same container as the scheduler, so it talks
// straight to pgBackRest and Postgres exactly like the scheduled path
// does) for operators inspecting recovery posture from the CLI, e.g.
// `docker compose exec backup-agent /app/service status`.
func runStatus(ctx context.Context) error {
	cfg, err := backupagent.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logger.New(logger.Config{Level: "info", Format: "json", AppName: "backup-agent"})
	agent := backupagent.NewAgent(cfg, log)
	st, err := agent.GetStatus(ctx)
	if err != nil {
		return fmt.Errorf("get status: %w", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(st)
}

// runBackupNow is an operator escape hatch — e.g.
// `docker compose exec backup-agent /app/service backup-full` right
// before a risky migration, without waiting for the next cron window.
// It calls the exact same Agent.RunBackup the scheduler itself calls
// (T2 Work item 1: "manual and scheduled paths use the same
// implementation"), just triggered by a human instead of pkg/scheduler.
func runBackupNow(ctx context.Context, backupType string) error {
	cfg, err := backupagent.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logger.New(logger.Config{Level: "info", Format: "json", AppName: "backup-agent"})
	agent := backupagent.NewAgent(cfg, log)
	return agent.RunBackup(ctx, backupType)
}

func run(parent context.Context) error {
	cfg, err := backupagent.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logger.New(logger.Config{Level: "info", Format: "json", AppName: "backup-agent"})

	certSrc, err := tlsx.LoadFromDir(cfg.TLSCertDir, "backup-agent", log)
	if err != nil {
		return fmt.Errorf("load TLS certificates: %w", err)
	}
	defer certSrc.Stop()

	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		log.Warn("Asia/Jakarta timezone database unavailable, scheduling in UTC instead", "error", err)
		loc = time.UTC
	}

	agent := backupagent.NewAgent(cfg, log)
	sched, err := agent.StartScheduler(loc)
	if err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}
	defer sched.Stop()

	server := &http.Server{
		Addr:              ":" + cfg.AppPort,
		Handler:           agent.NewMux(),
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    1 << 20,
		TLSConfig:         tlsx.ServerConfig(certSrc, []string{tlsx.IdentityDevOperator, tlsx.IdentityPrometheus}),
	}
	errCh := make(chan error, 1)
	go func() {
		if serveErr := server.ListenAndServeTLS("", ""); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()
	log.Info("backup-agent started", "http", server.Addr, "location", loc.String())

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errCh:
		cancel()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = err
	}
	return serveErr
}
