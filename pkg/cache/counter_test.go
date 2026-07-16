package cache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── MemoryCounter (docs/plan/17 Task T1) ──────────────────────────────────

func TestMemoryCounter_ImplementsInterface(t *testing.T) {
	var _ Counter = (*MemoryCounter)(nil)
}

func TestMemoryCounter_IncrByAccumulates(t *testing.T) {
	c := NewMemoryCounter()
	defer c.Stop()
	ctx := context.Background()

	total, err := c.IncrBy(ctx, "k", 100, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(100), total)

	total, err = c.IncrBy(ctx, "k", 50, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(150), total)
}

func TestMemoryCounter_GetReturnsZeroForAbsentKey(t *testing.T) {
	c := NewMemoryCounter()
	defer c.Stop()

	v, err := c.Get(context.Background(), "missing")
	require.NoError(t, err)
	assert.Zero(t, v)
}

func TestMemoryCounter_GetReflectsIncrBy(t *testing.T) {
	c := NewMemoryCounter()
	defer c.Stop()
	ctx := context.Background()

	_, err := c.IncrBy(ctx, "k", 42, time.Minute)
	require.NoError(t, err)

	v, err := c.Get(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, int64(42), v)
}

func TestMemoryCounter_ExpiresAfterTTL(t *testing.T) {
	c := NewMemoryCounter()
	defer c.Stop()
	ctx := context.Background()

	_, err := c.IncrBy(ctx, "k", 10, 20*time.Millisecond)
	require.NoError(t, err)

	v, err := c.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, int64(10), v)

	time.Sleep(30 * time.Millisecond)

	v, err = c.Get(ctx, "k")
	require.NoError(t, err)
	assert.Zero(t, v, "counter must reset once its TTL window has passed")
}

func TestMemoryCounter_IncrByAfterExpiryStartsFreshWindow(t *testing.T) {
	c := NewMemoryCounter()
	defer c.Stop()
	ctx := context.Background()

	_, err := c.IncrBy(ctx, "k", 10, 20*time.Millisecond)
	require.NoError(t, err)
	time.Sleep(30 * time.Millisecond)

	total, err := c.IncrBy(ctx, "k", 5, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total, "a write after the window expired must start a fresh window, not add to the stale total")
}

func TestMemoryCounter_ConcurrentIncrBy(t *testing.T) {
	c := NewMemoryCounter()
	defer c.Stop()
	ctx := context.Background()

	const goroutines = 50
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.IncrBy(ctx, "shared", 1, time.Minute)
		}()
	}
	wg.Wait()

	v, err := c.Get(ctx, "shared")
	require.NoError(t, err)
	assert.Equal(t, int64(goroutines), v, "-race must not report a data race, and the total must be exact")
}

func TestMemoryCounter_IndependentKeys(t *testing.T) {
	c := NewMemoryCounter()
	defer c.Stop()
	ctx := context.Background()

	_, err := c.IncrBy(ctx, "a", 10, time.Minute)
	require.NoError(t, err)
	_, err = c.IncrBy(ctx, "b", 20, time.Minute)
	require.NoError(t, err)

	va, err := c.Get(ctx, "a")
	require.NoError(t, err)
	vb, err := c.Get(ctx, "b")
	require.NoError(t, err)
	assert.Equal(t, int64(10), va)
	assert.Equal(t, int64(20), vb)
}
