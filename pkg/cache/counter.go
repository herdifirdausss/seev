package cache

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Counter is a TTL-windowed cumulative counter — the primitive
// internal/policy needs for daily/monthly velocity (docs/plan/17 Task T1):
// "how much has this user done in this window so far", not "how fast can
// they call this endpoint" (that's Limiter's job, a different shape).
// Redis-backed in production, in-memory when REDIS_ENABLED=false — same
// fallback pattern as Limiter/LockProvider elsewhere in this codebase.
type Counter interface {
	// IncrBy atomically adds delta to key's running total, setting ttl on
	// first creation only (an existing key's expiry is left untouched —
	// the window is anchored to its first write, not extended by every
	// subsequent one). Returns the new total.
	IncrBy(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error)

	// Get returns key's current total, 0 if absent.
	Get(ctx context.Context, key string) (int64, error)
}

// ─── RedisCounter ───────────────────────────────────────────────────────────

type RedisCounter struct {
	rdb *redis.Client
}

func NewRedisCounter(rdb *redis.Client) *RedisCounter {
	return &RedisCounter{rdb: rdb}
}

func (c *RedisCounter) IncrBy(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	pipe := c.rdb.TxPipeline()
	incr := pipe.IncrBy(ctx, key, delta)
	// NX: only set an expiry if the key doesn't already have one (first
	// write in this window) — a later write within the same window must
	// not push the expiry further out, or the window would never close.
	pipe.ExpireNX(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return incr.Val(), nil
}

func (c *RedisCounter) Get(ctx context.Context, key string) (int64, error) {
	v, err := c.rdb.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return v, nil
}

// ─── MemoryCounter ──────────────────────────────────────────────────────────

// MemoryCounter is an in-process fallback for single-node deployments
// without Redis (docs/plan/12 Task T1 pattern). NOT safe across multiple
// process replicas — each instance has its own independent counters.
type MemoryCounter struct {
	mu     sync.Mutex
	values map[string]counterEntry
	stop   chan struct{}
}

type counterEntry struct {
	total    int64
	expireAt time.Time
}

// memCounterGCInterval mirrors MemoryRateLimiter/MemoryLock's GC cadence —
// bounds unbounded map growth under a churning key space (many distinct
// users × transaction types × day/month windows).
const memCounterGCInterval = 5 * time.Minute

func NewMemoryCounter() *MemoryCounter {
	c := &MemoryCounter{
		values: make(map[string]counterEntry),
		stop:   make(chan struct{}),
	}
	go c.gcLoop()
	return c
}

func (c *MemoryCounter) gcLoop() {
	ticker := time.NewTicker(memCounterGCInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			c.mu.Lock()
			for k, v := range c.values {
				if now.After(v.expireAt) {
					delete(c.values, k)
				}
			}
			c.mu.Unlock()
		case <-c.stop:
			return
		}
	}
}

// Stop halts the GC goroutine — same symmetry note as MemoryRateLimiter.Stop.
func (c *MemoryCounter) Stop() {
	close(c.stop)
}

func (c *MemoryCounter) IncrBy(_ context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.values[key]
	if !ok || now.After(e.expireAt) {
		e = counterEntry{total: 0, expireAt: now.Add(ttl)}
	}
	e.total += delta
	c.values[key] = e
	return e.total, nil
}

func (c *MemoryCounter) Get(_ context.Context, key string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.values[key]
	if !ok || time.Now().After(e.expireAt) {
		return 0, nil
	}
	return e.total, nil
}
