package cache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ─── MemoryRateLimiter (docs/roadmap/archive/12 Task T1) ──────────────────────────────────

func TestMemoryRateLimiter_ImplementsInterface(t *testing.T) {
	var _ Limiter = (*MemoryRateLimiter)(nil)
}

func TestMemoryRateLimiter_AllowsWithinBurst(t *testing.T) {
	l := NewMemoryRateLimiter(RateConfig{Requests: 100, Per: time.Second, Burst: 3})
	defer l.Stop()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		allowed, _, err := l.Allow(ctx, "k")
		assert.NoError(t, err)
		assert.True(t, allowed, "request %d should be allowed within burst", i)
	}
}

func TestMemoryRateLimiter_RejectsOverBurst(t *testing.T) {
	l := NewMemoryRateLimiter(RateConfig{Requests: 1, Per: time.Minute, Burst: 2})
	defer l.Stop()
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		allowed, _, err := l.Allow(ctx, "k")
		assert.NoError(t, err)
		assert.True(t, allowed)
	}

	// Burst exhausted, refill rate is far too slow to have added a token yet.
	allowed, _, err := l.Allow(ctx, "k")
	assert.NoError(t, err)
	assert.False(t, allowed)
}

func TestMemoryRateLimiter_RefillsOverTime(t *testing.T) {
	// 1000 tokens/sec refill, burst 1 — after >1ms a token should be back.
	l := NewMemoryRateLimiter(RateConfig{Requests: 1000, Per: time.Second, Burst: 1})
	defer l.Stop()
	ctx := context.Background()

	allowed, _, err := l.Allow(ctx, "k")
	assert.NoError(t, err)
	assert.True(t, allowed)

	allowed, _, err = l.Allow(ctx, "k")
	assert.NoError(t, err)
	assert.False(t, allowed, "bucket should be empty immediately after consuming the only token")

	time.Sleep(5 * time.Millisecond)

	allowed, _, err = l.Allow(ctx, "k")
	assert.NoError(t, err)
	assert.True(t, allowed, "token should have refilled after waiting")
}

func TestMemoryRateLimiter_KeysAreIndependent(t *testing.T) {
	l := NewMemoryRateLimiter(RateConfig{Requests: 1, Per: time.Minute, Burst: 1})
	defer l.Stop()
	ctx := context.Background()

	allowed, _, err := l.Allow(ctx, "a")
	assert.NoError(t, err)
	assert.True(t, allowed)

	// Exhausting "a" must not affect "b".
	allowed, _, err = l.Allow(ctx, "b")
	assert.NoError(t, err)
	assert.True(t, allowed)
}

func TestMemoryRateLimiter_ConcurrentAccess_NoRace(t *testing.T) {
	l := NewMemoryRateLimiter(RateConfig{Requests: 1000, Per: time.Second, Burst: 1000})
	defer l.Stop()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _, _ = l.Allow(ctx, "shared-key")
			}
		}()
	}
	wg.Wait()
}

func TestNewMemoryRateLimiter_InvalidConfig_Panics(t *testing.T) {
	assert.Panics(t, func() {
		NewMemoryRateLimiter(RateConfig{Requests: 0, Per: time.Second, Burst: 1})
	})
}
