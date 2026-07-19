package cache

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultProbeInterval = 5 * time.Second
	defaultProbeTimeout  = 2 * time.Second
	// recoverThreshold is the number of CONSECUTIVE successful background
	// probes required before switching back to Redis (docs/plan/45 K4's
	// anti-flapping hysteresis) — a single lucky probe is not enough
	// evidence that a flapping Redis has actually stabilized.
	recoverThreshold = 2
)

// RedisHealthSwitcher tracks whether Redis is currently healthy for one
// primitive (docs/plan/45 Task T3/K4), shared by FailoverLimiter/
// FailoverCounter (route to a local in-memory fallback while unhealthy)
// and internal/fraud's FailClosedVelocityStore (returns a classified
// dependency error while unhealthy instead of a memory approximation —
// K4 explicitly rejects a memory fallback for fraud velocity).
//
// Degrade is IMMEDIATE: any real operation's failure marks the backend
// unhealthy the instant it happens, no waiting for the next probe tick.
// Recovery is delayed and hysteresis-gated: only the BACKGROUND probe loop
// (never a lucky real-call success) can mark the backend healthy again,
// and only after recoverThreshold consecutive successful probes — this is
// what prevents a flapping Redis from making every caller thrash between
// backends on every other call.
type RedisHealthSwitcher struct {
	primitive string
	ping      func(ctx context.Context) error
	logger    *slog.Logger

	probeInterval time.Duration
	probeTimeout  time.Duration

	healthy       atomic.Bool
	mu            sync.Mutex
	consecutiveOK int

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRedisHealthSwitcher constructs a switcher and immediately starts its
// background probe loop — mirrors MemoryRateLimiter/MemoryCounter's own
// "launch the goroutine in the constructor" convention rather than
// requiring a separate Start call. primitive is a fixed, low-cardinality
// label ("rate_limiter" | "policy_counter" | "fraud_velocity") for the
// redis_backend_active{primitive,backend} gauge (docs/plan/45 K6) — never
// derived from request input. ping should be a short, side-effect-free
// Redis health check (e.g. *redis.Client.Ping). Starts optimistically
// healthy: the first real operation's own outcome is what actually proves
// or disproves that.
func NewRedisHealthSwitcher(primitive string, ping func(ctx context.Context) error, logger *slog.Logger) *RedisHealthSwitcher {
	return newRedisHealthSwitcher(primitive, ping, logger, defaultProbeInterval, defaultProbeTimeout)
}

// newRedisHealthSwitcher is NewRedisHealthSwitcher's actual constructor,
// parameterized on probe timing — unexported and only for this package's
// own tests to get fast, deterministic probe cadences without racily
// mutating probeInterval/probeTimeout AFTER the background goroutine
// (started here) has already begun reading them.
func newRedisHealthSwitcher(primitive string, ping func(ctx context.Context) error, logger *slog.Logger, probeInterval, probeTimeout time.Duration) *RedisHealthSwitcher {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &RedisHealthSwitcher{
		primitive: primitive, ping: ping, logger: logger,
		probeInterval: probeInterval, probeTimeout: probeTimeout,
		cancel: cancel,
	}
	s.healthy.Store(true)
	redisBackendActive.WithLabelValues(primitive, "redis").Set(1)
	redisBackendActive.WithLabelValues(primitive, "local").Set(0)
	s.wg.Add(1)
	go s.probeLoop(ctx)
	return s
}

// Stop halts the background probe loop and waits for it to exit — call on
// shutdown, and in tests that construct many switchers, to avoid leaking
// goroutines (same rationale as MemoryRateLimiter.Stop/MemoryCounter.Stop).
func (s *RedisHealthSwitcher) Stop() {
	s.cancel()
	s.wg.Wait()
}

// Healthy reports whether callers should currently prefer Redis.
func (s *RedisHealthSwitcher) Healthy() bool {
	return s.healthy.Load()
}

// Degrade marks the backend unhealthy immediately — call after a real
// Redis operation fails. A no-op if already degraded (besides resetting
// the recovery hysteresis counter), and logs/updates the gauge only on the
// actual healthy->unhealthy transition, never per call.
func (s *RedisHealthSwitcher) Degrade(cause error) {
	s.mu.Lock()
	s.consecutiveOK = 0
	s.mu.Unlock()

	if s.healthy.CompareAndSwap(true, false) {
		s.logger.Warn("cache: redis backend degraded", slog.String("primitive", s.primitive), slog.Any("error", cause))
		redisBackendActive.WithLabelValues(s.primitive, "redis").Set(0)
		redisBackendActive.WithLabelValues(s.primitive, "local").Set(1)
	}
}

func (s *RedisHealthSwitcher) probeLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.probeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.probeOnce(ctx)
		}
	}
}

// probeOnce only ever attempts to RECOVER (local -> redis) — a healthy
// switcher has nothing to probe for; its own real operations are what
// detect degradation, immediately, via Degrade.
func (s *RedisHealthSwitcher) probeOnce(ctx context.Context) {
	if s.healthy.Load() {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, s.probeTimeout)
	err := s.ping(pctx)
	cancel()
	if err != nil {
		// A failed probe must reset the streak, not just skip incrementing
		// it — otherwise a flapping Redis (success, fail, success, ...)
		// could "leak" past the 2-consecutive-success requirement one
		// success at a time, defeating the anti-flapping hysteresis this
		// counter exists for.
		s.mu.Lock()
		s.consecutiveOK = 0
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	s.consecutiveOK++
	reached := s.consecutiveOK >= recoverThreshold
	s.mu.Unlock()

	if reached && s.healthy.CompareAndSwap(false, true) {
		s.logger.Info("cache: redis backend recovered", slog.String("primitive", s.primitive))
		redisBackendActive.WithLabelValues(s.primitive, "redis").Set(1)
		redisBackendActive.WithLabelValues(s.primitive, "local").Set(0)
	}
}

// ─── FailoverLimiter ────────────────────────────────────────────────────────

// FailoverLimiter implements Limiter, routing to Redis while healthy and
// to an embedded MemoryRateLimiter while degraded (docs/plan/45 Task T3/
// K4) — unlike DistributedBreaker/fraud's FailClosedVelocityStore, a rate
// limiter's memory fallback is an accepted, documented weakening (each
// replica enforces its own independent bucket), not a money-safety
// concern, so falling back rather than failing closed is correct here.
type FailoverLimiter struct {
	switcher *RedisHealthSwitcher
	redis    Limiter
	local    *MemoryRateLimiter
}

// NewFailoverLimiter constructs a limiter that starts probing rdb
// immediately (via the embedded RedisHealthSwitcher). Call Stop on
// shutdown to release both the probe loop and the local limiter's own GC
// goroutine.
func NewFailoverLimiter(rdb *redis.Client, cfg RateConfig, logger *slog.Logger) *FailoverLimiter {
	return &FailoverLimiter{
		switcher: NewRedisHealthSwitcher("rate_limiter", func(ctx context.Context) error { return rdb.Ping(ctx).Err() }, logger),
		redis:    NewRedisRateLimiter(rdb, cfg),
		local:    NewMemoryRateLimiter(cfg),
	}
}

func (f *FailoverLimiter) Stop() {
	f.switcher.Stop()
	f.local.Stop()
}

func (f *FailoverLimiter) Allow(ctx context.Context, key string) (bool, int64, error) {
	if !f.switcher.Healthy() {
		return f.local.Allow(ctx, key)
	}
	allowed, remaining, err := f.redis.Allow(ctx, key)
	if err != nil {
		f.switcher.Degrade(err)
		return f.local.Allow(ctx, key)
	}
	return allowed, remaining, nil
}

var _ Limiter = (*FailoverLimiter)(nil)

// ─── FailoverCounter ────────────────────────────────────────────────────────

// FailoverCounter implements Counter, routing to Redis while healthy and
// to an embedded MemoryCounter while degraded (docs/plan/45 Task T3/K4).
// The memory fallback is per-replica and can only ever OVER-count
// leniently relative to a true cross-replica total during an outage
// (docs/plan/45 K4: "dapat memperbesar allowance saat outage") — since
// internal/policy's existing behavior on a counter error is already
// fail-open, this is a strictly stronger degradation than today's
// no-enforcement-at-all gap, but it is NOT a substitute for real
// cross-replica enforcement and must never be marketed as one.
type FailoverCounter struct {
	switcher *RedisHealthSwitcher
	redis    Counter
	local    *MemoryCounter
}

// NewFailoverCounter constructs a counter that starts probing rdb
// immediately. Call Stop on shutdown.
func NewFailoverCounter(rdb *redis.Client, logger *slog.Logger) *FailoverCounter {
	return &FailoverCounter{
		switcher: NewRedisHealthSwitcher("policy_counter", func(ctx context.Context) error { return rdb.Ping(ctx).Err() }, logger),
		redis:    NewRedisCounter(rdb),
		local:    NewMemoryCounter(),
	}
}

func (f *FailoverCounter) Stop() {
	f.switcher.Stop()
	f.local.Stop()
}

func (f *FailoverCounter) IncrBy(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	if !f.switcher.Healthy() {
		return f.local.IncrBy(ctx, key, delta, ttl)
	}
	total, err := f.redis.IncrBy(ctx, key, delta, ttl)
	if err != nil {
		f.switcher.Degrade(err)
		return f.local.IncrBy(ctx, key, delta, ttl)
	}
	return total, nil
}

func (f *FailoverCounter) Get(ctx context.Context, key string) (int64, error) {
	if !f.switcher.Healthy() {
		return f.local.Get(ctx, key)
	}
	total, err := f.redis.Get(ctx, key)
	if err != nil {
		f.switcher.Degrade(err)
		return f.local.Get(ctx, key)
	}
	return total, nil
}

var _ Counter = (*FailoverCounter)(nil)
