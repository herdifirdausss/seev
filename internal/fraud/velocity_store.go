package fraud

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/herdifirdausss/seev/internal/fraud/model"
	"github.com/herdifirdausss/seev/pkg/cache"
)

// VelocityStore atomically deduplicates a posted event and increments its
// user's hourly counter. It also supplies the read side used by the rule.
type VelocityStore interface {
	Get(context.Context, string) (int64, error)
	Record(ctx context.Context, eventID, counterKey string, ttl time.Duration) error
}

type RedisVelocityStore struct{ client *redis.Client }

func NewRedisVelocityStore(client *redis.Client) *RedisVelocityStore {
	return &RedisVelocityStore{client: client}
}

func (s *RedisVelocityStore) Get(ctx context.Context, key string) (int64, error) {
	value, err := s.client.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return value, err
}

var recordVelocityScript = redis.NewScript(`
if redis.call('SET', KEYS[1], '1', 'EX', ARGV[1], 'NX') then
  local count = redis.call('INCR', KEYS[2])
  if count == 1 then redis.call('EXPIRE', KEYS[2], ARGV[1]) end
  return 1
end
return 0`)

func (s *RedisVelocityStore) Record(ctx context.Context, eventID, counterKey string, ttl time.Duration) error {
	dedupKey := "fraud:velocity:event:" + eventID
	if _, err := recordVelocityScript.Run(ctx, s.client, []string{dedupKey, counterKey}, int64(ttl.Seconds())).Int64(); err != nil {
		return fmt.Errorf("record velocity: %w", err)
	}
	return nil
}

// ─── FailClosedVelocityStore ────────────────────────────────────────────────

// FailClosedVelocityStore wraps RedisVelocityStore with a background
// Redis-health probe (docs/plan/45 Task T3/K4) — unlike
// pkg/cache.FailoverLimiter/FailoverCounter, there is NO memory fallback
// here: a memory approximation of fraud velocity could silently weaken
// screening thresholds, so while Redis is unhealthy every call fails
// closed with model.ErrDependencyUnavailable instead of attempting Redis
// (and paying its timeout) or falling back to an in-process guess. This is
// what lets fraud-service start and keep serving amount-threshold
// screening even when Redis is down at boot or fails mid-flight, while
// still refusing to silently under-screen for velocity specifically.
// redisAttemptTimeout bounds a live Redis attempt taken while the
// switcher is still (possibly stale-)Healthy — Degrade is immediate on a
// real failure, but only AFTER that failure is observed, and there is a
// window right after Redis actually goes down, before Degrade fires, where
// Healthy() still reports true. Without its own bound here, that one
// attempt inherits the CALLER's full remaining deadline — pkg/fraudcheck's
// screenTimeout budgets the ENTIRE round trip (network out + this call +
// classify + respond + network back) at 500ms total, so a slow-to-fail
// connection attempt (go-redis's own dial/retry timeouts run well past
// 150ms) can consume the whole budget, leaving no time for a properly
// classified DEPENDENCY_UNAVAILABLE response to reach the caller before
// ITS deadline also expires — that raw context.DeadlineExceeded doesn't
// match ErrDependencyUnavailable, so the caller falls through to fraud's
// fail-OPEN branch instead of fail-closed (found live by docs/plan/49 T6's
// isolated GATE 3 run — chaos scenario 9 flagged this).  150ms leaves
// generous room for the rest of that 500ms round trip even in the worst
// case, while remaining far above any latency a genuinely healthy
// same-network Redis would ever need to answer GET/EVALSHA.
const redisAttemptTimeout = 150 * time.Millisecond

type FailClosedVelocityStore struct {
	switcher *cache.RedisHealthSwitcher
	redis    VelocityStore
}

// NewFailClosedVelocityStore constructs a store whose background probe
// starts immediately (docs/plan/45 K4: fraud-service may start without
// Redis and keeps probing). Call Stop on shutdown.
func NewFailClosedVelocityStore(client *redis.Client, logger *slog.Logger) *FailClosedVelocityStore {
	return &FailClosedVelocityStore{
		switcher: cache.NewRedisHealthSwitcher("fraud_velocity", func(ctx context.Context) error { return client.Ping(ctx).Err() }, logger),
		redis:    NewRedisVelocityStore(client),
	}
}

func (s *FailClosedVelocityStore) Stop() {
	s.switcher.Stop()
}

func (s *FailClosedVelocityStore) Get(ctx context.Context, key string) (int64, error) {
	if !s.switcher.Healthy() {
		return 0, model.ErrDependencyUnavailable
	}
	attemptCtx, cancel := context.WithTimeout(ctx, redisAttemptTimeout)
	defer cancel()
	v, err := s.redis.Get(attemptCtx, key)
	if err != nil {
		s.switcher.Degrade(err)
		return 0, model.ErrDependencyUnavailable
	}
	return v, nil
}

func (s *FailClosedVelocityStore) Record(ctx context.Context, eventID, counterKey string, ttl time.Duration) error {
	if !s.switcher.Healthy() {
		return model.ErrDependencyUnavailable
	}
	attemptCtx, cancel := context.WithTimeout(ctx, redisAttemptTimeout)
	defer cancel()
	if err := s.redis.Record(attemptCtx, eventID, counterKey, ttl); err != nil {
		s.switcher.Degrade(err)
		return model.ErrDependencyUnavailable
	}
	return nil
}

var _ VelocityStore = (*FailClosedVelocityStore)(nil)
