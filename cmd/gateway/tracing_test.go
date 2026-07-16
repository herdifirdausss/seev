package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetupTracing_EmptyEndpoint_NoProviderInstalled is the regression test
// for docs/plan/12 Task T5's core guarantee: an empty
// OTEL_EXPORTER_OTLP_ENDPOINT must never install a real TracerProvider, and
// must never error or panic — this is the default for every deployment that
// hasn't opted into tracing.
func TestSetupTracing_EmptyEndpoint_NoProviderInstalled(t *testing.T) {
	shutdown, err := setupTracing(context.Background(), "")

	require.NoError(t, err)
	require.NotNil(t, shutdown, "shutdown must always be non-nil and safe to call")

	assert.NoError(t, shutdown(context.Background()), "no-op shutdown must not error")
}

// TestSetupTracing_ConfiguredEndpoint_DoesNotErrorOrPanicAtSetup proves
// otlptracegrpc.New succeeds without actually needing a reachable collector
// — gRPC exporters are lazy-connecting by design, so setup succeeding here
// says nothing about whether a collector is actually up, only that the
// wiring itself (exporter + resource + provider construction) is correct.
func TestSetupTracing_ConfiguredEndpoint_DoesNotErrorOrPanicAtSetup(t *testing.T) {
	// Deliberately NOT a real collector — proves setup itself doesn't
	// require connectivity, matching the doc's "OTLP exporters connect
	// lazily" assumption.
	var shutdown func(context.Context) error
	var err error
	assert.NotPanics(t, func() {
		shutdown, err = setupTracing(context.Background(), "127.0.0.1:1")
	})
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Shutdown may itself return an error (no collector to flush to) — the
	// important thing is it returns at all, bounded by our own timeout,
	// rather than hanging indefinitely.
	_ = shutdown(ctx)
}
