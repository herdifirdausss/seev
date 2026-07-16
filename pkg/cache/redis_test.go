package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newTestCache(t *testing.T) (*Cache, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	return NewFromClient(client), mr
}

// ─── Interface compliance ─────────────────────────────────────────────────────

func TestCache_ImplementsInterface(t *testing.T) {
	var _ Cacher = (*Cache)(nil)
}

func TestMockCache_ImplementsInterface(t *testing.T) {
	var _ Cacher = (*MockCache)(nil)
}

// ─── NewFromClient ────────────────────────────────────────────────────────────

func TestNewFromClient(t *testing.T) {
	c, _ := newTestCache(t)
	assert.NotNil(t, c)
	assert.NotNil(t, c.Client())
}

// ─── HealthCheck ──────────────────────────────────────────────────────────────

func TestCache_HealthCheck_Success(t *testing.T) {
	c, _ := newTestCache(t)
	err := c.HealthCheck(context.Background())
	assert.NoError(t, err)
}

func TestCache_HealthCheck_Failure(t *testing.T) {
	c, mr := newTestCache(t)
	mr.Close() // kill the server

	err := c.HealthCheck(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ping")
}

// ─── Close ────────────────────────────────────────────────────────────────────

func TestCache_Close(t *testing.T) {
	c, _ := newTestCache(t)
	err := c.Close()
	assert.NoError(t, err)
}

// ─── Set / Get ────────────────────────────────────────────────────────────────

func TestCache_Set_Get_Success(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	type Payload struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	original := Payload{Name: "Alice", Age: 30}
	err := c.Set(ctx, "user:1", original, time.Minute)
	require.NoError(t, err)

	var result Payload
	err = c.Get(ctx, "user:1", &result)
	require.NoError(t, err)
	assert.Equal(t, original, result)
}

func TestCache_Set_WithZeroTTL(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()
	// 0 TTL means no expiry
	err := c.Set(ctx, "permanent", "value", 0)
	assert.NoError(t, err)

	var v string
	err = c.Get(ctx, "permanent", &v)
	assert.NoError(t, err)
	assert.Equal(t, "value", v)
}

func TestCache_Set_MarshalError(t *testing.T) {
	c, _ := newTestCache(t)
	// channels can't be marshaled to JSON
	err := c.Set(context.Background(), "key", make(chan int), time.Minute)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "marshal")
}

func TestCache_Get_Miss(t *testing.T) {
	c, _ := newTestCache(t)

	var v string
	err := c.Get(context.Background(), "nonexistent", &v)
	assert.ErrorIs(t, err, ErrCacheMiss)
}

func TestCache_Get_CorruptData(t *testing.T) {
	c, mr := newTestCache(t)
	// Manually set invalid JSON
	require.NoError(t, mr.Set("badkey", "not-valid-json"))

	var v map[string]any
	err := c.Get(context.Background(), "badkey", &v)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestCache_Get_RedisError(t *testing.T) {
	c, mr := newTestCache(t)
	mr.Close()

	var v string
	err := c.Get(context.Background(), "key", &v)
	assert.Error(t, err)
}

// ─── Delete ───────────────────────────────────────────────────────────────────

func TestCache_Delete(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "k1", "v1", time.Minute))
	require.NoError(t, c.Set(ctx, "k2", "v2", time.Minute))

	err := c.Delete(ctx, "k1", "k2")
	require.NoError(t, err)

	var v string
	assert.ErrorIs(t, c.Get(ctx, "k1", &v), ErrCacheMiss)
	assert.ErrorIs(t, c.Get(ctx, "k2", &v), ErrCacheMiss)
}

func TestCache_Delete_RedisError(t *testing.T) {
	c, mr := newTestCache(t)
	mr.Close()

	err := c.Delete(context.Background(), "key")
	assert.Error(t, err)
}

// ─── Exists ───────────────────────────────────────────────────────────────────

func TestCache_Exists(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	exists, err := c.Exists(ctx, "missing")
	require.NoError(t, err)
	assert.False(t, exists)

	require.NoError(t, c.Set(ctx, "present", "val", time.Minute))

	exists, err = c.Exists(ctx, "present")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestCache_Exists_RedisError(t *testing.T) {
	c, mr := newTestCache(t)
	mr.Close()

	_, err := c.Exists(context.Background(), "key")
	assert.Error(t, err)
}

// ─── SetNX ────────────────────────────────────────────────────────────────────

func TestCache_SetNX(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	// First call sets the key
	ok, err := c.SetNX(ctx, "lock", "1", time.Minute)
	require.NoError(t, err)
	assert.True(t, ok)

	// Second call fails because key exists
	ok, err = c.SetNX(ctx, "lock", "1", time.Minute)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestCache_SetNX_MarshalError(t *testing.T) {
	c, _ := newTestCache(t)
	_, err := c.SetNX(context.Background(), "key", make(chan int), time.Minute)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "marshal")
}

func TestCache_SetNX_RedisError(t *testing.T) {
	c, mr := newTestCache(t)
	mr.Close()

	_, err := c.SetNX(context.Background(), "key", "val", time.Minute)
	assert.Error(t, err)
}

// ─── IncrWithExpiry ───────────────────────────────────────────────────────────

func TestCache_IncrWithExpiry(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	n, err := c.IncrWithExpiry(ctx, "counter", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	n, err = c.IncrWithExpiry(ctx, "counter", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}

func TestCache_IncrWithExpiry_RedisError(t *testing.T) {
	c, mr := newTestCache(t)
	mr.Close()

	_, err := c.IncrWithExpiry(context.Background(), "counter", time.Minute)
	assert.Error(t, err)
}

// ─── GetOrSet ─────────────────────────────────────────────────────────────────

func TestCache_GetOrSet_CacheHit(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	// Pre-populate cache
	require.NoError(t, c.Set(ctx, "item:1", map[string]any{"id": float64(1)}, time.Minute))

	fetchCalled := false
	var dest map[string]any
	err := c.GetOrSet(ctx, "item:1", time.Minute, &dest, func() (any, error) {
		fetchCalled = true
		return nil, nil
	})
	require.NoError(t, err)
	assert.False(t, fetchCalled)
	assert.Equal(t, float64(1), dest["id"])
}

func TestCache_GetOrSet_CacheMiss_FetchSuccess(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	var dest map[string]any
	err := c.GetOrSet(ctx, "item:1", time.Minute, &dest, func() (any, error) {
		return map[string]any{"id": float64(42)}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, float64(42), dest["id"])

	// Second call should hit cache
	fetchCalled := false
	var dest2 map[string]any
	err = c.GetOrSet(ctx, "item:1", time.Minute, &dest2, func() (any, error) {
		fetchCalled = true
		return nil, nil
	})
	require.NoError(t, err)
	assert.False(t, fetchCalled)
}

func TestCache_GetOrSet_FetchError(t *testing.T) {
	c, _ := newTestCache(t)
	wantErr := errors.New("fetch error")

	var dest string
	err := c.GetOrSet(context.Background(), "missing", time.Minute, &dest, func() (any, error) {
		return nil, wantErr
	})
	assert.ErrorIs(t, err, wantErr)
}

func TestCache_GetOrSet_FetchUnmarshalable(t *testing.T) {
	c, _ := newTestCache(t)

	var dest string
	// Return a channel — can't be marshaled
	err := c.GetOrSet(context.Background(), "key", time.Minute, &dest, func() (any, error) {
		return make(chan int), nil
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "marshal")
}

func TestCache_GetOrSet_StoreFailsButReturnsValue(t *testing.T) {
	// c, mr := newTestCache(t)
	ctx := context.Background()

	var dest string
	// The fetch succeeds and fills dest. Then the server dies for the Set call.
	// We need to ensure dest is filled before Close.
	// Strategy: use a custom cache that fails on Set but succeeds on Get (miss scenario).
	// Simplest: run GetOrSet first with server up (fetch runs), then verify result.
	// Actually to test the slog.Warn path, we need Set to fail AFTER fetch.
	// We can do this by closing miniredis between fetch and set by using a slow fetch.
	// Instead, test via a wrapping approach: close mr after set, which will not affect
	// the returned dest since dest is filled from fetch.

	// Test: fetch returns value, Set fails (server down mid-test).
	// We test this by doing an initial fetch where we Close the server,
	// which means Get returns a network error (not ErrCacheMiss).
	// So we need a different approach.

	// Create a cache that always returns ErrCacheMiss on Get but fails on Set.
	// We can do this by stopping miniredis after the Get check.
	// Use a closure with a counter:
	callCount := 0
	_ = callCount

	// Direct test: close server to make Set fail, but dest is already populated from fetch.
	// We achieve this by having the fetch fn close the server.
	mr2, err := miniredis.Run()
	require.NoError(t, err)

	client2 := redis.NewClient(&redis.Options{Addr: mr2.Addr()})
	c2 := NewFromClient(client2)

	err = c2.GetOrSet(ctx, "key", time.Minute, &dest, func() (any, error) {
		mr2.Close() // Kill server so subsequent Set fails → triggers slog.Warn path
		return "fetched-value", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "fetched-value", dest)
}

func TestCache_GetOrSet_GetError_NonMiss(t *testing.T) {
	c, mr := newTestCache(t)
	mr.Close() // Force a connection error so Get returns a non-miss error

	var dest string
	err := c.GetOrSet(context.Background(), "key", time.Minute, &dest, func() (any, error) {
		return "val", nil
	})
	assert.Error(t, err)
}

// ─── MockCache ────────────────────────────────────────────────────────────────

func TestMockCache_AllMethods_DefaultNil(t *testing.T) {
	m := &MockCache{}
	ctx := context.Background()

	assert.NoError(t, m.Set(ctx, "k", "v", time.Minute))
	assert.NoError(t, m.Get(ctx, "k", new(string)))
	assert.NoError(t, m.Delete(ctx, "k"))
	ok, err := m.Exists(ctx, "k")
	assert.NoError(t, err)
	assert.False(t, ok)
	set, err := m.SetNX(ctx, "k", "v", time.Minute)
	assert.NoError(t, err)
	assert.True(t, set)
	n, err := m.IncrWithExpiry(ctx, "k", time.Minute)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.NoError(t, m.GetOrSet(ctx, "k", time.Minute, new(string), nil))
	assert.NoError(t, m.HealthCheck(ctx))
	assert.NoError(t, m.Close())
}

func TestMockCache_AllMethods_WithFunctions(t *testing.T) {
	wantErr := errors.New("mock error")
	ctx := context.Background()

	m := &MockCache{
		SetFn:            func(_ context.Context, _ string, _ any, _ time.Duration) error { return wantErr },
		GetFn:            func(_ context.Context, _ string, _ any) error { return wantErr },
		DeleteFn:         func(_ context.Context, _ ...string) error { return wantErr },
		ExistsFn:         func(_ context.Context, _ string) (bool, error) { return true, wantErr },
		SetNXFn:          func(_ context.Context, _ string, _ any, _ time.Duration) (bool, error) { return false, wantErr },
		IncrWithExpiryFn: func(_ context.Context, _ string, _ time.Duration) (int64, error) { return 99, wantErr },
		GetOrSetFn:       func(_ context.Context, _ string, _ time.Duration, _ any, _ func() (any, error)) error { return wantErr },
		HealthCheckFn:    func(_ context.Context) error { return wantErr },
		CloseFn:          func() error { return wantErr },
	}

	assert.ErrorIs(t, m.Set(ctx, "", nil, 0), wantErr)
	assert.ErrorIs(t, m.Get(ctx, "", nil), wantErr)
	assert.ErrorIs(t, m.Delete(ctx), wantErr)

	ok, err := m.Exists(ctx, "")
	assert.True(t, ok)
	assert.ErrorIs(t, err, wantErr)

	set, err := m.SetNX(ctx, "", nil, 0)
	assert.False(t, set)
	assert.ErrorIs(t, err, wantErr)

	n, err := m.IncrWithExpiry(ctx, "", 0)
	assert.Equal(t, int64(99), n)
	assert.ErrorIs(t, err, wantErr)

	assert.ErrorIs(t, m.GetOrSet(ctx, "", 0, nil, nil), wantErr)
	assert.ErrorIs(t, m.HealthCheck(ctx), wantErr)
	assert.ErrorIs(t, m.Close(), wantErr)
}
