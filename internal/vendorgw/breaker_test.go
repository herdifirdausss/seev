package vendorgw

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthTracker_ClosedByDefault_AlwaysAllows(t *testing.T) {
	tr := NewHealthTracker(5, 30*time.Second, nil)
	assert.True(t, tr.Allow(context.Background(), "v1"))
	snap := tr.Snapshot(context.Background())
	require.Len(t, snap, 1)
	assert.Equal(t, StateClosed, snap[0].State)
}

func TestHealthTracker_ThresholdTripsToOpen(t *testing.T) {
	tr := NewHealthTracker(3, 30*time.Second, nil)
	for i := 0; i < 2; i++ {
		tr.RecordFailure(context.Background(), "v1")
		assert.True(t, tr.Allow(context.Background(), "v1"), "must stay closed before threshold is reached")
	}
	tr.RecordFailure(context.Background(), "v1")
	assert.False(t, tr.Allow(context.Background(), "v1"), "must open exactly at the threshold")

	snap := tr.Snapshot(context.Background())
	require.Len(t, snap, 1)
	assert.Equal(t, StateOpen, snap[0].State)
	assert.Equal(t, 3, snap[0].ConsecutiveFailures)
	assert.False(t, snap[0].OpenedAt.IsZero())
}

func TestHealthTracker_OpenBeforeCooldown_StillDisallows(t *testing.T) {
	tr := NewHealthTracker(1, time.Hour, nil)
	tr.RecordFailure(context.Background(), "v1")
	assert.False(t, tr.Allow(context.Background(), "v1"), "cooldown has not elapsed yet")
}

func TestHealthTracker_HalfOpenProbe_SuccessClosesCircuit(t *testing.T) {
	tr := NewHealthTracker(1, 10*time.Millisecond, nil)
	tr.RecordFailure(context.Background(), "v1")
	require.False(t, tr.Allow(context.Background(), "v1"))
	time.Sleep(20 * time.Millisecond)

	require.True(t, tr.Allow(context.Background(), "v1"), "cooldown elapsed — this call becomes the probe")
	tr.RecordSuccess(context.Background(), "v1")

	snap := tr.Snapshot(context.Background())
	require.Len(t, snap, 1)
	assert.Equal(t, StateClosed, snap[0].State)
	assert.Zero(t, snap[0].ConsecutiveFailures)
	assert.True(t, tr.Allow(context.Background(), "v1"))
}

func TestHealthTracker_HalfOpenProbe_FailureReopensImmediately(t *testing.T) {
	tr := NewHealthTracker(1, 10*time.Millisecond, nil)
	tr.RecordFailure(context.Background(), "v1")
	time.Sleep(20 * time.Millisecond)
	require.True(t, tr.Allow(context.Background(), "v1"), "cooldown elapsed — this call becomes the probe")

	tr.RecordFailure(context.Background(), "v1") // the probe itself failed
	assert.False(t, tr.Allow(context.Background(), "v1"), "must re-open on a failed probe — no waiting to re-accumulate the threshold")

	snap := tr.Snapshot(context.Background())
	require.Len(t, snap, 1)
	assert.Equal(t, StateOpen, snap[0].State)
}

// TestHealthTracker_HalfOpenSingleProbe_RaceSafe is the DoD's "half-open
// probe single-flight (two goroutines -> one probe)" requirement — run
// with -race. Many goroutines call Allow concurrently right as the
// cooldown elapses; exactly one must observe true.
func TestHealthTracker_HalfOpenSingleProbe_RaceSafe(t *testing.T) {
	tr := NewHealthTracker(1, 10*time.Millisecond, nil)
	tr.RecordFailure(context.Background(), "v1")
	time.Sleep(20 * time.Millisecond)

	const goroutines = 20
	var allowedCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if tr.Allow(context.Background(), "v1") {
				allowedCount.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), allowedCount.Load(), "exactly one goroutine must win the single probe slot")
}

// TestHealthTracker_RecordSuccess_NeverTrips proves the breaker-side half
// of gotcha #13 (business rejections must not trip the breaker): the
// CALLER decides whether a business rejection maps to RecordSuccess (it
// must, per docs/roadmap/archive/40 Task T1 Step #3) — from the tracker's own
// point of view, repeated RecordSuccess calls (however many "business
// rejections" they represent) must never open the circuit.
func TestHealthTracker_RecordSuccess_NeverTrips(t *testing.T) {
	tr := NewHealthTracker(2, 30*time.Second, nil)
	for i := 0; i < 50; i++ {
		tr.RecordSuccess(context.Background(), "v1")
	}
	assert.True(t, tr.Allow(context.Background(), "v1"))
	snap := tr.Snapshot(context.Background())
	require.Len(t, snap, 1)
	assert.Equal(t, StateClosed, snap[0].State)
	assert.Zero(t, snap[0].ConsecutiveFailures)
}

func TestHealthTracker_RecordSuccess_ResetsCounterBeforeThreshold(t *testing.T) {
	tr := NewHealthTracker(3, 30*time.Second, nil)
	tr.RecordFailure(context.Background(), "v1")
	tr.RecordFailure(context.Background(), "v1")
	tr.RecordSuccess(context.Background(), "v1")
	tr.RecordFailure(context.Background(), "v1")
	tr.RecordFailure(context.Background(), "v1")
	assert.True(t, tr.Allow(context.Background(), "v1"), "the reset must mean two more failures alone don't reach the threshold of 3")
}

func TestHealthTracker_Snapshot_MultipleVendorsSortedAndAccurate(t *testing.T) {
	tr := NewHealthTracker(1, 30*time.Second, nil)
	tr.RecordFailure(context.Background(), "zeta")
	tr.RecordSuccess(context.Background(), "alpha")

	snap := tr.Snapshot(context.Background())
	require.Len(t, snap, 2)
	assert.Equal(t, "alpha", snap[0].Vendor)
	assert.Equal(t, StateClosed, snap[0].State)
	assert.Equal(t, "zeta", snap[1].Vendor)
	assert.Equal(t, StateOpen, snap[1].State)
}

func TestNewHealthTracker_DefaultsAppliedForZeroValues(t *testing.T) {
	tr := NewHealthTracker(0, 0, nil)
	assert.Equal(t, 5, tr.failureThreshold)
	assert.Equal(t, 30*time.Second, tr.cooldown)
}
