package server

import (
	"net/http"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/config"
)

// internal/server/server_test.go
func TestGracefulShutdown(t *testing.T) {
	cfg := config.AppConfig{
		Port:            "0", // random port
		ShutdownTimeout: 5 * time.Second,
	}
	srv := New(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cleanupCalled := make(chan struct{})
	errCh := make(chan error, 1)

	go func() {
		errCh <- srv.StartWithSignals(func() {
			close(cleanupCalled)
		}, syscall.SIGUSR1) // inject custom signal untuk test
	}()

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR1))

	select {
	case <-cleanupCalled:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup not called")
	}

	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestStartMulti_GracefulShutdown verifies two independent listeners (as
// used for the public + internal ledger routers, docs/plan/10 Task T1) both
// drain cleanly and cleanup runs exactly once, on a single shared signal.
func TestStartMulti_GracefulShutdown(t *testing.T) {
	cfg := config.AppConfig{Port: "0", ShutdownTimeout: 5 * time.Second}

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv1 := New(cfg, okHandler)
	srv2 := New(cfg, okHandler)

	cleanupCalls := int32(0)
	errCh := make(chan error, 1)

	go func() {
		errCh <- StartMultiWithSignals(func() {
			atomic.AddInt32(&cleanupCalls, 1)
		}, []os.Signal{syscall.SIGUSR2}, srv1, srv2)
	}()

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR2))

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StartMulti did not return after shutdown signal")
	}

	assert.Equal(t, int32(1), atomic.LoadInt32(&cleanupCalls), "cleanup must run exactly once")
}
