package fraud

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/fraud/model"
)

func newTestFailClosedStore(t *testing.T) (*FailClosedVelocityStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewFailClosedVelocityStore(client, nil)
	t.Cleanup(store.Stop)
	return store, mr
}

func TestFailClosedVelocityStore_ImplementsInterface(t *testing.T) {
	var _ VelocityStore = (*FailClosedVelocityStore)(nil)
}

func TestFailClosedVelocityStore_HealthyRedis_WorksNormally(t *testing.T) {
	store, _ := newTestFailClosedStore(t)
	ctx := context.Background()

	require.NoError(t, store.Record(ctx, "evt-1", "user:key", time.Hour))
	count, err := store.Get(ctx, "user:key")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// TestFailClosedVelocityStore_RedisDown_FailsClosed_NeverMemoryApproximation
// proves docs/plan/45 K4's core safety property: while Redis is down,
// velocity operations return model.ErrDependencyUnavailable — never a
// zero/default value, and never a memory-based approximation.
func TestFailClosedVelocityStore_RedisDown_FailsClosed_NeverMemoryApproximation(t *testing.T) {
	store, mr := newTestFailClosedStore(t)
	ctx := context.Background()

	mr.Close()

	_, err := store.Get(ctx, "user:key")
	require.Error(t, err)
	assert.ErrorIs(t, err, model.ErrDependencyUnavailable)

	err = store.Record(ctx, "evt-1", "user:key", time.Hour)
	require.Error(t, err)
	assert.ErrorIs(t, err, model.ErrDependencyUnavailable)
}

func TestFailClosedVelocityStore_RedisDown_SubsequentCallsFailFastWithoutRetryingRedis(t *testing.T) {
	store, mr := newTestFailClosedStore(t)
	ctx := context.Background()

	mr.Close()
	_, err := store.Get(ctx, "user:key")
	require.ErrorIs(t, err, model.ErrDependencyUnavailable)

	// Once degraded, Get must not even attempt Redis again — it should
	// return immediately from the switcher's cached Healthy() check, not
	// re-pay a connection-timeout cost on every call.
	start := time.Now()
	_, err = store.Get(ctx, "user:key")
	elapsed := time.Since(start)
	require.ErrorIs(t, err, model.ErrDependencyUnavailable)
	assert.Less(t, elapsed, 50*time.Millisecond, "a degraded store must fail fast, not re-attempt Redis per call")
}

// Recovery hysteresis itself (two consecutive successful background
// probes before switching back) is the SHARED cache.RedisHealthSwitcher
// this store embeds — thoroughly proven by pkg/cache's own test suite
// (TestRedisHealthSwitcher_RecoversAfterTwoConsecutiveProbes et al.), not
// re-tested here to avoid duplicating a slow, timing-sensitive test across
// packages for a mechanism this package doesn't reimplement.

// TestErrDependencyUnavailable_SurvivesWrapping proves the mechanism
// VelocityAnomalyRule.Screen actually relies on: wrapping
// model.ErrDependencyUnavailable with %w (as it does today —
// fmt.Errorf("velocity counter: %w", err)) keeps it errors.Is-detectable
// all the way up through internal/fraud/grpcserver; a plain string
// concatenation would silently break that chain.
func TestErrDependencyUnavailable_SurvivesWrapping(t *testing.T) {
	wrapped := fmt.Errorf("velocity counter: %w", model.ErrDependencyUnavailable)
	assert.True(t, errors.Is(wrapped, model.ErrDependencyUnavailable))

	notWrapped := errors.New("velocity counter: " + model.ErrDependencyUnavailable.Error())
	assert.False(t, errors.Is(notWrapped, model.ErrDependencyUnavailable), "sanity: a plain string-concatenated error must NOT satisfy errors.Is")
}
