package vendorgw

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDistributedBreaker(t *testing.T, failureThreshold int, cooldown, probeTTL time.Duration) (*DistributedBreaker, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewDistributedBreaker(client, "testns", failureThreshold, cooldown, probeTTL, nil), mr
}

func TestDistributedBreaker_ImplementsInterface(t *testing.T) {
	var _ Breaker = (*DistributedBreaker)(nil)
}

func TestDistributedBreaker_ClosedByDefault(t *testing.T) {
	d, _ := newTestDistributedBreaker(t, 5, 30*time.Second, 0)
	assert.True(t, d.Allow(context.Background(), "v1"))
}

func TestDistributedBreaker_OpensAtThreshold(t *testing.T) {
	d, _ := newTestDistributedBreaker(t, 3, 30*time.Second, 0)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		d.RecordFailure(ctx, "v1")
		assert.True(t, d.Allow(ctx, "v1"), "must stay closed before threshold is reached")
	}
	d.RecordFailure(ctx, "v1")
	assert.False(t, d.Allow(ctx, "v1"), "must open exactly at the threshold")
}

func TestDistributedBreaker_CooldownGatesHalfOpen(t *testing.T) {
	d, _ := newTestDistributedBreaker(t, 1, time.Hour, 0)
	ctx := context.Background()
	d.RecordFailure(ctx, "v1")
	assert.False(t, d.Allow(ctx, "v1"), "cooldown has not elapsed yet")
}

func TestDistributedBreaker_HalfOpenProbeSucceeds_Closes(t *testing.T) {
	d, _ := newTestDistributedBreaker(t, 1, 10*time.Millisecond, time.Second)
	ctx := context.Background()
	d.RecordFailure(ctx, "v1")
	require.False(t, d.Allow(ctx, "v1"))
	time.Sleep(20 * time.Millisecond)
	require.True(t, d.Allow(ctx, "v1"), "cooldown elapsed — this call becomes the probe")
	d.RecordSuccess(ctx, "v1")
	assert.True(t, d.Allow(ctx, "v1"))
}

func TestDistributedBreaker_HalfOpenProbeFails_ReopensWithoutReaccumulating(t *testing.T) {
	d, _ := newTestDistributedBreaker(t, 1, 10*time.Millisecond, time.Second)
	ctx := context.Background()
	d.RecordFailure(ctx, "v1")
	time.Sleep(20 * time.Millisecond)
	require.True(t, d.Allow(ctx, "v1"), "cooldown elapsed — this call becomes the probe")
	d.RecordFailure(ctx, "v1") // the probe itself failed
	assert.False(t, d.Allow(ctx, "v1"), "must re-open on a failed probe — no waiting to re-accumulate the threshold")
}

func TestDistributedBreaker_ExpiredProbeToken_AllowsFreshProbe(t *testing.T) {
	// probeTTL shorter than cooldown: once the token expires, a NEW caller
	// must be able to claim the probe slot again — proves a crashed
	// prober's slot self-heals instead of wedging the vendor open forever.
	// The probe token's expiry is a real Redis TTL, not a wall-clock
	// comparison our own Lua does — miniredis only advances TTLs via
	// FastForward, never via real time.Sleep, so that's used here instead.
	d, mr := newTestDistributedBreaker(t, 1, 10*time.Millisecond, 30*time.Millisecond)
	ctx := context.Background()
	d.RecordFailure(ctx, "v1")
	time.Sleep(20 * time.Millisecond)
	require.True(t, d.Allow(ctx, "v1"), "first caller wins the probe")
	assert.False(t, d.Allow(ctx, "v1"), "a second caller must be denied while the probe token is live")
	mr.FastForward(40 * time.Millisecond) // probe token expires without ever resolving
	assert.True(t, d.Allow(ctx, "v1"), "an expired, unresolved probe token must free the slot for a fresh attempt")
}

// TestDistributedBreaker_ConcurrentHalfOpenCallers_ExactlyOneProbeWins is
// docs/plan/45 Task T2's required concurrency proof: N callers racing the
// SAME cooldown-elapsed vendor must yield exactly one true (the probe),
// enforced by the SET NX PX probe token — never zero, never more than one,
// regardless of how many goroutines call Allow at once.
func TestDistributedBreaker_ConcurrentHalfOpenCallers_ExactlyOneProbeWins(t *testing.T) {
	d, _ := newTestDistributedBreaker(t, 1, 5*time.Millisecond, time.Second)
	ctx := context.Background()
	d.RecordFailure(ctx, "v1")
	time.Sleep(15 * time.Millisecond)

	const concurrency = 20
	var wonCount int64
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d.Allow(ctx, "v1") {
				atomic.AddInt64(&wonCount, 1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(1), wonCount, "exactly one of %d concurrent Allow callers must win the probe slot", concurrency)
}

// TestDistributedBreaker_TwoInstancesShareRedis_StateConverges is
// docs/plan/45 Task T2's cross-replica convergence proof: two SEPARATE
// DistributedBreaker instances (simulating two payout-service replicas)
// sharing the same Redis must see each other's writes — a failure recorded
// via instance A must open the circuit for instance B too, without B ever
// having recorded a failure of its own locally.
func TestDistributedBreaker_TwoInstancesShareRedis_StateConverges(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })

	a := NewDistributedBreaker(clientA, "testns", 1, time.Hour, 0, nil)
	b := NewDistributedBreaker(clientB, "testns", 1, time.Hour, 0, nil)
	ctx := context.Background()

	require.True(t, b.Allow(ctx, "v1"), "closed by default")
	a.RecordFailure(ctx, "v1")
	assert.False(t, b.Allow(ctx, "v1"), "instance B must see the circuit A just opened, via shared Redis state")
}

// TestDistributedBreaker_TwoInstancesConcurrentHalfOpen_ExactlyOneProbeWins
// is docs/plan/45 Task T2's literal gate requirement: "dua tracker yang
// berbagi Redis dan N caller half-open bersamaan: tepat satu probe lintas
// instance" — two SEPARATE DistributedBreaker instances (two simulated
// replicas), each firing concurrent Allow calls at the SAME vendor once
// its shared cooldown has elapsed, must still yield exactly one winning
// probe across BOTH instances combined, never one winner per instance.
func TestDistributedBreaker_TwoInstancesConcurrentHalfOpen_ExactlyOneProbeWins(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })

	a := NewDistributedBreaker(clientA, "testns", 1, 5*time.Millisecond, time.Second, nil)
	b := NewDistributedBreaker(clientB, "testns", 1, 5*time.Millisecond, time.Second, nil)
	ctx := context.Background()

	a.RecordFailure(ctx, "v1")
	time.Sleep(15 * time.Millisecond)

	const perInstance = 10
	var wonCount int64
	var wg sync.WaitGroup
	fire := func(d *DistributedBreaker) {
		defer wg.Done()
		if d.Allow(ctx, "v1") {
			atomic.AddInt64(&wonCount, 1)
		}
	}
	for i := 0; i < perInstance; i++ {
		wg.Add(2)
		go fire(a)
		go fire(b)
	}
	wg.Wait()

	assert.Equal(t, int64(1), wonCount, "exactly one probe must win across BOTH instances combined (%d callers each)", perInstance)
}

// TestDistributedBreaker_RedisDown_FallsBackToLocal_NoErrorPropagates
// proves docs/plan/45 K3's core safety property: a Redis error must NEVER
// propagate into the payout/payin request path — Allow/RecordSuccess/
// RecordFailure/Snapshot all silently degrade to the embedded local
// HealthTracker instead.
func TestDistributedBreaker_RedisDown_FallsBackToLocal_NoErrorPropagates(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	d := NewDistributedBreaker(client, "testns", 1, time.Hour, 0, nil)
	ctx := context.Background()

	mr.Close() // Redis is now unreachable for the rest of this test

	assert.True(t, d.Allow(ctx, "v1"), "must fall back to the local tracker's closed-by-default state")
	assert.NotPanics(t, func() { d.RecordFailure(ctx, "v1") })
	assert.False(t, d.Allow(ctx, "v1"), "local fallback must have opened after RecordFailure at threshold 1")
	assert.NotPanics(t, func() { d.RecordSuccess(ctx, "v1") })
	assert.NotPanics(t, func() { d.Snapshot(ctx) })
}

// TestDistributedBreaker_BackendGauge_LogsOnlyOnTransition proves the
// backend gauge reflects the CURRENT backend after every call, but a
// steady run against the same backend doesn't re-trigger the (untestable
// via a gauge alone, but this at least proves the gauge itself tracks
// reality) transition path more than once per actual degrade/recover.
func TestDistributedBreaker_BackendGauge_ReflectsCurrentBackend(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	d := NewDistributedBreaker(client, "gaugetest", 1, time.Hour, 0, nil)
	ctx := context.Background()

	d.Allow(ctx, "v1")
	redisVal := testGaugeValue(t, "gaugetest", "redis")
	localVal := testGaugeValue(t, "gaugetest", "local")
	assert.Equal(t, 1.0, redisVal)
	assert.Equal(t, 0.0, localVal)

	mr.Close()
	d.Allow(ctx, "v1")
	redisVal = testGaugeValue(t, "gaugetest", "redis")
	localVal = testGaugeValue(t, "gaugetest", "local")
	assert.Equal(t, 0.0, redisVal)
	assert.Equal(t, 1.0, localVal)
}

func testGaugeValue(t *testing.T, namespace, backend string) float64 {
	t.Helper()
	return testutil.ToFloat64(breakerBackend.WithLabelValues(namespace, backend))
}
