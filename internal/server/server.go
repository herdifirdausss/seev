package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/herdifirdausss/seev/internal/config"
)

// Server wraps net/http.Server with graceful shutdown support.
type Server struct {
	httpServer *http.Server
	cfg        config.AppConfig
}

// New creates a new HTTP server bound to all interfaces on cfg.Port.
// Hardened timeouts and MaxHeaderBytes are applied.
func New(cfg config.AppConfig, handler http.Handler) *Server {
	return NewWithAddr(cfg, ":"+cfg.Port, handler)
}

// NewWithAddr is like New but binds to an explicit address instead of
// deriving one from cfg.Port — used for the internal-only listener
// (docs/plan/10 Task T1), which binds to 127.0.0.1 by default rather than
// all interfaces.
func NewWithAddr(cfg config.AppConfig, addr string, handler http.Handler) *Server {
	return &Server{
		cfg: cfg,
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadTimeout:       cfg.ReadTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
			ReadHeaderTimeout: 5 * time.Second,
			MaxHeaderBytes:    1 << 20,
		},
	}
}

// NewWithAddrTLS is like NewWithAddr but serves mutual TLS using tlsConfig
// (docs/plan/49 K6) — used for the internal-only listener once its peers
// are required to present a client certificate. listenAndServe below calls
// ListenAndServeTLS("", "") when this is set; empty cert/key file
// arguments are correct because tlsConfig.GetCertificate (built via
// pkg/tlsx) already supplies the certificate.
func NewWithAddrTLS(cfg config.AppConfig, addr string, handler http.Handler, tlsConfig *tls.Config) *Server {
	s := NewWithAddr(cfg, addr, handler)
	s.httpServer.TLSConfig = tlsConfig
	return s
}

// startWithSignals is the testable inner implementation; callers can inject signals.
func (s *Server) StartWithSignals(cleanup func(), sigs ...os.Signal) error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, sigs...)
	defer signal.Stop(quit)

	serverErr := s.listenAndServe()

	// Block until signal or fatal error
	select {
	case err := <-serverErr:
		return err
	case sig := <-quit:
		slog.Info("server: shutdown signal received", "signal", sig.String())
	}

	if err := s.shutdown(); err != nil {
		return err
	}

	// Run cleanup after server has stopped accepting connections
	if cleanup != nil {
		slog.Info("server: running cleanup")
		cleanup()
	}

	slog.Info("server: stopped cleanly")
	return nil
}

// listenAndServe starts the underlying http.Server in the background and
// returns a channel that receives at most one error — a startup/runtime
// failure, or nothing if the server later shuts down cleanly via shutdown().
func (s *Server) listenAndServe() <-chan error {
	errCh := make(chan error, 1)
	go func() {
		slog.Info("server: listening", "addr", s.httpServer.Addr, "env", s.cfg.Env, "tls", s.httpServer.TLSConfig != nil)
		var err error
		if s.httpServer.TLSConfig != nil {
			err = s.httpServer.ListenAndServeTLS("", "")
		} else {
			err = s.httpServer.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("server: listen and serve: %w", err)
		}
	}()
	return errCh
}

// shutdown gracefully stops the server using the configured ShutdownTimeout.
func (s *Server) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	slog.Info("server: shutting down", "addr", s.httpServer.Addr, "timeout", s.cfg.ShutdownTimeout)
	if err := s.httpServer.Shutdown(ctx); err != nil {
		slog.Error("server: forced shutdown", "addr", s.httpServer.Addr, "error", err)
		return fmt.Errorf("server shutdown (%s): %w", s.httpServer.Addr, err)
	}
	return nil
}

// StartMulti runs several servers concurrently and blocks until SIGINT or
// SIGTERM is received (or one of them fails to start). On shutdown, every
// server is gracefully drained before cleanup runs exactly once — this is
// what wires the public and internal ledger listeners together
// (docs/plan/10 Task T1; see cmd/gateway/main.go).
func StartMulti(cleanup func(), servers ...*Server) error {
	return StartMultiWithSignals(cleanup, []os.Signal{syscall.SIGINT, syscall.SIGTERM}, servers...)
}

// StartMultiWithSignals is the testable inner implementation of StartMulti;
// callers can inject signals (mirrors Server.StartWithSignals).
func StartMultiWithSignals(cleanup func(), sigs []os.Signal, servers ...*Server) error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, sigs...)
	defer signal.Stop(quit)

	// Fan-in: any server's startup/runtime error is treated as fatal for
	// the whole process, same as the single-server Start path.
	fatal := make(chan error, len(servers))
	for _, s := range servers {
		errCh := s.listenAndServe()
		go func() {
			if err := <-errCh; err != nil {
				fatal <- err
			}
		}()
	}

	select {
	case err := <-fatal:
		return err
	case sig := <-quit:
		slog.Info("server: shutdown signal received", "signal", sig.String())
	}

	for _, s := range servers {
		if err := s.shutdown(); err != nil {
			// Keep draining the rest even if one server's shutdown failed —
			// still want to attempt cleanup rather than leave dependencies
			// (DB pool, MQ channels) open on a half-shutdown process.
			slog.Error("server: shutdown error, continuing to drain remaining listeners", "error", err)
		}
	}

	if cleanup != nil {
		slog.Info("server: running cleanup")
		cleanup()
	}

	slog.Info("server: stopped cleanly")
	return nil
}
