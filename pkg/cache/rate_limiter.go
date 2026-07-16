package cache

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	_ "embed"

	"github.com/redis/go-redis/v9"
)

type Limiter interface {
	Allow(ctx context.Context, key string) (bool, int64, error)
}

type RateConfig struct {
	Requests int
	Per      time.Duration
	Burst    int
}

//go:embed rate_limiter.lua
var luaScript string

type RedisRateLimiter struct {
	rdb      *redis.Client
	script   *redis.Script
	rate     float64
	capacity float64
}

func NewRedisRateLimiter(
	rdb *redis.Client,
	cfg RateConfig,
) *RedisRateLimiter {

	if cfg.Requests <= 0 || cfg.Per <= 0 {
		panic("invalid rate limiter config")
	}

	ratePerMs := float64(cfg.Requests) / float64(cfg.Per.Milliseconds())

	return &RedisRateLimiter{
		rdb:      rdb,
		script:   redis.NewScript(luaScript),
		rate:     ratePerMs,
		capacity: float64(cfg.Burst),
	}
}

func (r *RedisRateLimiter) Allow(
	ctx context.Context,
	key string,
) (bool, int64, error) {

	now := time.Now().UnixMilli()

	res, err := r.script.Run(
		ctx,
		r.rdb,
		[]string{key},
		r.rate,
		r.capacity,
		now,
		1,
	).Result()

	if err != nil {
		return false, 0, err
	}

	values, ok := res.([]any)
	if !ok || len(values) < 2 {
		return false, 0, errors.New("invalid redis rate limiter response")
	}

	allowed := values[0].(int64) == 1
	remaining := int64(toFloat64(values[1]))

	return allowed, remaining, nil
}

func toFloat64(v any) float64 {
	switch t := v.(type) {
	case int64:
		return float64(t)
	case float64:
		return t
	default:
		return 0
	}
}

// ─── MemoryRateLimiter ─────────────────────────────────────────────────────

// MemoryRateLimiter is an in-process token-bucket limiter for single-node
// deployments that run without Redis (docs/plan/12 Task T1, REDIS_ENABLED=
// false). It implements the exact same algorithm as rate_limiter.lua so
// behavior is identical whichever backend is active — only the storage
// differs. NOT safe across multiple process replicas: each instance has its
// own independent bucket per key.
type MemoryRateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]bucketState
	rate     float64 // tokens per millisecond
	capacity float64
	stop     chan struct{}
}

type bucketState struct {
	tokens float64
	lastMs int64
}

// memLimiterGCInterval controls how often stale buckets are purged so the
// map doesn't grow unbounded under a churning key space (e.g. RateLimitByIP
// keys for many distinct client IPs) — mirrors the pattern in
// pkg/scheduler.MemoryLock.
const memLimiterGCInterval = 5 * time.Minute

func NewMemoryRateLimiter(cfg RateConfig) *MemoryRateLimiter {
	if cfg.Requests <= 0 || cfg.Per <= 0 {
		panic("invalid rate limiter config")
	}
	l := &MemoryRateLimiter{
		buckets:  make(map[string]bucketState),
		rate:     float64(cfg.Requests) / float64(cfg.Per.Milliseconds()),
		capacity: float64(cfg.Burst),
		stop:     make(chan struct{}),
	}
	go l.gcLoop()
	return l
}

func (l *MemoryRateLimiter) gcLoop() {
	ticker := time.NewTicker(memLimiterGCInterval)
	defer ticker.Stop()
	// A bucket that hasn't been touched for long enough to fully refill is
	// indistinguishable from a fresh one — safe to drop.
	staleAfter := time.Duration(math.Ceil(l.capacity/l.rate)) * time.Millisecond * 2

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-staleAfter).UnixMilli()
			l.mu.Lock()
			for k, b := range l.buckets {
				if b.lastMs < cutoff {
					delete(l.buckets, k)
				}
			}
			l.mu.Unlock()
		case <-l.stop:
			return
		}
	}
}

// Stop halts the GC goroutine. Not required for correctness (the limiter is
// only ever constructed once at startup and lives for the process
// lifetime), provided for symmetry with pkg/scheduler.MemoryLock and to
// avoid leaking a goroutine in tests that construct many limiters.
func (l *MemoryRateLimiter) Stop() {
	close(l.stop)
}

func (l *MemoryRateLimiter) Allow(_ context.Context, key string) (bool, int64, error) {
	now := time.Now().UnixMilli()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = bucketState{tokens: l.capacity, lastMs: now}
	}

	delta := now - b.lastMs
	if delta < 0 {
		delta = 0
	}
	tokens := math.Min(l.capacity, b.tokens+float64(delta)*l.rate)

	allowed := tokens >= 1
	if allowed {
		tokens -= 1
	}

	l.buckets[key] = bucketState{tokens: tokens, lastMs: now}
	return allowed, int64(tokens), nil
}
