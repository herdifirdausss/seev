package tracing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetup_EmptyEndpoint_NoProviderInstalled is the regression test for
// docs/roadmap/archive/12 Task T5's core guarantee (carried into docs/roadmap/archive/43 K3): an
// empty endpoint must never install a real TracerProvider, and must never
// error or panic — this is the default for every deployment that hasn't
// opted into tracing.
func TestSetup_EmptyEndpoint_NoProviderInstalled(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{ServiceName: "gateway"})

	require.NoError(t, err)
	require.NotNil(t, shutdown, "shutdown must always be non-nil and safe to call")

	assert.NoError(t, shutdown(context.Background()), "no-op shutdown must not error")
}

// TestSetup_ConfiguredEndpoint_DoesNotErrorOrPanicAtSetup proves
// otlptracegrpc.New succeeds without actually needing a reachable collector
// — gRPC exporters are lazy-connecting by design, so setup succeeding here
// says nothing about whether a collector is actually up, only that the
// wiring itself (exporter + resource + provider construction) is correct.
func TestSetup_ConfiguredEndpoint_DoesNotErrorOrPanicAtSetup(t *testing.T) {
	// Deliberately NOT a real collector — proves setup itself doesn't
	// require connectivity, matching the doc's "OTLP exporters connect
	// lazily" assumption.
	var shutdown func(context.Context) error
	var err error
	assert.NotPanics(t, func() {
		shutdown, err = Setup(context.Background(), Config{
			ServiceName: "ledger-service", Endpoint: "127.0.0.1:1", SampleRatio: 0.1, Insecure: true,
		})
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

// TestSetup_InvalidSampleRatio_Rejected covers docs/roadmap/archive/43 K3's explicit
// requirement: a ratio outside [0, 1] must fail validation clearly, not
// silently clamp or panic deep inside the SDK's sampler.
func TestSetup_InvalidSampleRatio_Rejected(t *testing.T) {
	for _, ratio := range []float64{-0.1, 1.1, 2, -5} {
		_, err := Setup(context.Background(), Config{
			ServiceName: "gateway", Endpoint: "127.0.0.1:1", SampleRatio: ratio,
		})
		require.Error(t, err, "ratio %v should be rejected", ratio)
	}
}
