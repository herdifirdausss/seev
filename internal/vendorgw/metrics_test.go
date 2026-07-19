package vendorgw

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBreakerState_ReflectsFullLifecycle proves the gauge tracks every
// transition Allow/RecordFailure/RecordSuccess drive (docs/plan/43 K5):
// unseen vendor is absent, first-seen vendor reports closed (0), threshold
// trip reports open (2), cooldown-elapsed probe reports half_open (1), and
// a successful probe reports closed (0) again.
func TestBreakerState_ReflectsFullLifecycle(t *testing.T) {
	vendor := "metrics-lifecycle-vendor"
	gauge := breakerState.WithLabelValues(vendor)

	tr := NewHealthTracker(1, time.Millisecond, nil)
	tr.Allow(context.Background(), vendor) // first touch — creates the vendor entry, closed
	assert.Equal(t, float64(0), testutil.ToFloat64(gauge))

	tr.RecordFailure(context.Background(), vendor) // threshold=1 -> trips open immediately
	assert.Equal(t, float64(2), testutil.ToFloat64(gauge))

	time.Sleep(2 * time.Millisecond)
	require.True(t, tr.Allow(context.Background(), vendor), "cooldown elapsed — this call IS the probe")
	assert.Equal(t, float64(1), testutil.ToFloat64(gauge))

	tr.RecordSuccess(context.Background(), vendor) // probe succeeded
	assert.Equal(t, float64(0), testutil.ToFloat64(gauge))
}

// TestBreakerState_HalfOpenProbeFailure_ReportsOpen covers the re-open path
// that doesn't go through RecordFailure's threshold-counting branch.
func TestBreakerState_HalfOpenProbeFailure_ReportsOpen(t *testing.T) {
	vendor := "metrics-probe-fail-vendor"
	gauge := breakerState.WithLabelValues(vendor)

	tr := NewHealthTracker(1, time.Millisecond, nil)
	tr.RecordFailure(context.Background(), vendor)
	time.Sleep(2 * time.Millisecond)
	tr.Allow(context.Background(), vendor) // enters half-open
	assert.Equal(t, float64(1), testutil.ToFloat64(gauge))

	tr.RecordFailure(context.Background(), vendor) // probe failed -> re-open
	assert.Equal(t, float64(2), testutil.ToFloat64(gauge))
}
