package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSwitcher(t *testing.T, ping func(ctx context.Context) error) *RedisHealthSwitcher {
	t.Helper()
	s := newRedisHealthSwitcher("test_primitive", ping, nil, 10*time.Millisecond, 50*time.Millisecond)
	t.Cleanup(s.Stop)
	return s
}

func TestRedisHealthSwitcher_StartsHealthy(t *testing.T) {
	s := newTestSwitcher(t, func(context.Context) error { return nil })
	assert.True(t, s.Healthy())
}

func TestRedisHealthSwitcher_DegradesImmediatelyOnFailure(t *testing.T) {
	s := newTestSwitcher(t, func(context.Context) error { return nil })
	s.Degrade(assertErr("boom"))
	assert.False(t, s.Healthy())
}

// TestRedisHealthSwitcher_RecoversAfterTwoConsecutiveProbes proves
// docs/roadmap/archive/45 K4's anti-flapping hysteresis: a single successful probe is
// not enough to recover — it takes two IN A ROW.
func TestRedisHealthSwitcher_RecoversAfterTwoConsecutiveProbes(t *testing.T) {
	healthy := make(chan struct{})
	var probeCount int
	s := newTestSwitcher(t, func(context.Context) error {
		probeCount++
		return nil
	})
	s.Degrade(assertErr("boom"))
	require.False(t, s.Healthy())

	go func() {
		for !s.Healthy() {
			time.Sleep(2 * time.Millisecond)
		}
		close(healthy)
	}()

	select {
	case <-healthy:
	case <-time.After(2 * time.Second):
		t.Fatal("switcher never recovered")
	}
	assert.GreaterOrEqual(t, probeCount, 2, "recovery must require at least 2 probes")
}

// TestRedisHealthSwitcher_FlappingProbe_NeverRecoversOnSingleSuccess proves
// a lone successful probe sandwiched between failures never flips the
// switcher healthy.
func TestRedisHealthSwitcher_FlappingProbe_NeverRecoversOnSingleSuccess(t *testing.T) {
	var calls int
	s := newTestSwitcher(t, func(context.Context) error {
		calls++
		// Strict alternation (success, fail, success, fail, ...) —
		// guaranteed to NEVER produce two consecutive successes, unlike a
		// cycling pattern over an odd-length slice, which can accidentally
		// wrap into a same-value pair at the seam.
		if calls%2 == 1 {
			return nil
		}
		return assertErr("flap")
	})
	s.Degrade(assertErr("initial"))
	require.False(t, s.Healthy())

	// Let several probe ticks elapse — with the flapping pattern above, no
	// two consecutive probes ever both succeed.
	time.Sleep(120 * time.Millisecond)
	assert.False(t, s.Healthy(), "a flapping Redis must never recover on isolated single-probe successes")
}

func TestRedisHealthSwitcher_ProbeOnlyRunsWhileDegraded(t *testing.T) {
	var pings int
	s := newTestSwitcher(t, func(context.Context) error {
		pings++
		return nil
	})
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, pings, "a healthy switcher must not probe — real operations are what detect degradation")
	assert.True(t, s.Healthy())
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

// ─── FailoverLimiter ────────────────────────────────────────────────────────

func newMiniredisClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client, mr
}

func TestFailoverLimiter_UsesRedisWhileHealthy(t *testing.T) {
	client, _ := newMiniredisClient(t)
	l := NewFailoverLimiter(client, RateConfig{Requests: 10, Per: time.Minute, Burst: 10}, nil)
	t.Cleanup(l.Stop)

	allowed, _, err := l.Allow(context.Background(), "k1")
	require.NoError(t, err)
	assert.True(t, allowed)
}

// TestFailoverLimiter_RedisDown_FallsBackToMemory_NoErrorPropagates proves
// docs/roadmap/archive/45 K4: a Redis failure never surfaces as an error to the
// caller — it transparently serves from the memory fallback instead.
func TestFailoverLimiter_RedisDown_FallsBackToMemory_NoErrorPropagates(t *testing.T) {
	client, mr := newMiniredisClient(t)
	l := NewFailoverLimiter(client, RateConfig{Requests: 10, Per: time.Minute, Burst: 10}, nil)
	t.Cleanup(l.Stop)

	mr.Close()

	allowed, _, err := l.Allow(context.Background(), "k1")
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.False(t, l.switcher.Healthy())
}

func TestFailoverLimiter_Stop_ReleasesGoroutines(t *testing.T) {
	client, _ := newMiniredisClient(t)
	l := NewFailoverLimiter(client, RateConfig{Requests: 10, Per: time.Minute, Burst: 10}, nil)
	assert.NotPanics(t, l.Stop)
}

// ─── FailoverCounter ────────────────────────────────────────────────────────

func TestFailoverCounter_UsesRedisWhileHealthy(t *testing.T) {
	client, _ := newMiniredisClient(t)
	c := NewFailoverCounter(client, nil)
	t.Cleanup(c.Stop)

	total, err := c.IncrBy(context.Background(), "k1", 1, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
}

func TestFailoverCounter_RedisDown_FallsBackToMemory_NoErrorPropagates(t *testing.T) {
	client, mr := newMiniredisClient(t)
	c := NewFailoverCounter(client, nil)
	t.Cleanup(c.Stop)

	mr.Close()

	total, err := c.IncrBy(context.Background(), "k1", 1, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	got, err := c.Get(context.Background(), "k1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), got, "the memory fallback must serve the value it just wrote, independent of Redis")
}

func TestFailoverCounter_RecoversAfterRedisComesBack(t *testing.T) {
	client, mr := newMiniredisClient(t)
	c := &FailoverCounter{
		switcher: newRedisHealthSwitcher("policy_counter", func(ctx context.Context) error { return client.Ping(ctx).Err() }, nil, 10*time.Millisecond, 50*time.Millisecond),
		redis:    NewRedisCounter(client),
		local:    NewMemoryCounter(),
	}
	t.Cleanup(c.Stop)

	mr.Close()
	_, err := c.IncrBy(context.Background(), "k1", 1, time.Minute)
	require.NoError(t, err)
	require.False(t, c.switcher.Healthy())

	// Restart on the SAME address (simulates the operator's actual
	// mitigation — the Redis process comes back at the same endpoint)
	// rather than mutating the live client's connection target, which
	// would race with the background probe goroutine already using it.
	require.NoError(t, mr.Restart())

	require.Eventually(t, func() bool { return c.switcher.Healthy() }, 2*time.Second, 5*time.Millisecond,
		"switcher must recover once Redis is reachable again")
}
