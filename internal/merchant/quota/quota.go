// Package quota is internal/merchant's per-tenant rate-limit enforcement
// (docs/roadmap/archive/57-c1-merchant-b2b-api.md §3.1, T4). It reuses
// pkg/cache's existing atomic Redis token-bucket Lua script
// (pkg/cache.RedisRateLimiter) rather than writing a second one — the only
// thing this package adds is PER-TENANT dynamic rate/burst (the shared
// pkg/cache type bakes its RateConfig in at construction, which doesn't fit
// a policy loaded per (tenant, quota_class) at request time).
package quota

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/pkg/cache"
)

// ErrQuotaBackendUnavailable is returned by Check for a write-class check
// when Redis cannot be reached — callers MUST fail the request closed
// (503 QUOTA_UNAVAILABLE) rather than allow it (T4 acceptance: "Redis
// outage blocks financial writes").
var ErrQuotaBackendUnavailable = errors.New("merchant/quota: quota backend unavailable")

// defaultPolicy is used when a tenant has no merchant_quota_policies row
// for the requested class — a conservative secure default (§ MerchantConfig's
// own "secure defaults" mandate), not unlimited access by omission.
var defaultPolicy = model.QuotaPolicy{RequestsPerMinute: 60, Burst: 60, IsEnabled: true}

// redisPingTimeout bounds how long Check waits to detect a dead/degraded
// Redis before falling back — same "don't let a half-dead dependency eat
// the caller's whole deadline" principle as TM-14's fraud velocity fix
// (150ms sub-deadline), scaled up slightly since this check also does a
// real EVAL round-trip, not just a HMGET.
const redisPingTimeout = 200 * time.Millisecond

// Result is one quota check's outcome, carrying everything the HTTP layer
// needs to set §6.3's RateLimit-*/Retry-After response headers.
type Result struct {
	Allowed      bool
	Limit        int64
	Remaining    int64
	ResetSeconds int64
	// Degraded is true only for a READ check that fell back because the
	// quota backend was unreachable (§4's "read fallback is bounded and
	// observable") — never true for a write check, since a write either
	// gets a real Redis-backed decision or is rejected outright.
	Degraded bool
}

// Enforcer checks and enforces per-tenant, per-quota-class quotas.
type Enforcer struct {
	policies repository.QuotaRepository
	redis    *redis.Client
}

func NewEnforcer(policies repository.QuotaRepository, redisClient *redis.Client) *Enforcer {
	if policies == nil {
		panic("merchant/quota: NewEnforcer requires a non-nil QuotaRepository")
	}
	return &Enforcer{policies: policies, redis: redisClient}
}

// Check enforces the tenant's quota for quotaClass. isWrite selects the
// failure posture on backend unavailability: fail-closed (write) vs a
// bounded, explicitly degraded allow (read) — see ErrQuotaBackendUnavailable
// and Result.Degraded.
func (e *Enforcer) Check(ctx context.Context, tenantID uuid.UUID, quotaClass string, isWrite bool) (Result, error) {
	policy, err := e.policies.GetByTenantAndClass(ctx, tenantID, quotaClass)
	if errors.Is(err, repository.ErrNotFound) {
		policy = defaultPolicy
	} else if err != nil {
		return Result{}, fmt.Errorf("merchant/quota: load policy: %w", err)
	}

	if !policy.IsEnabled {
		// A disabled POLICY row means "not currently enforced for this
		// tenant/class" — an operator kill-switch for a specific quota,
		// not a blanket "reject everything" (that's TENANT_SUSPENDED,
		// a tenant-level state, handled entirely separately in T3's auth
		// middleware).
		return Result{Allowed: true, Limit: int64(policy.RequestsPerMinute), Remaining: int64(policy.Burst)}, nil
	}

	if e.redis == nil {
		return e.fallback(isWrite, policy)
	}

	pingCtx, cancel := context.WithTimeout(ctx, redisPingTimeout)
	defer cancel()
	limiter := cache.NewRedisRateLimiter(e.redis, cache.RateConfig{
		Requests: policy.RequestsPerMinute, Per: time.Minute, Burst: policy.Burst,
	})
	quotaKey := fmt.Sprintf("merchant:quota:%s:%s", tenantID, quotaClass)
	allowed, remaining, err := limiter.Allow(pingCtx, quotaKey)
	if err != nil {
		return e.fallback(isWrite, policy)
	}

	resetSeconds := int64(0)
	if remaining < int64(policy.Burst) {
		shortfall := float64(policy.Burst) - float64(remaining)
		ratePerSecond := float64(policy.RequestsPerMinute) / 60.0
		resetSeconds = int64(math.Ceil(shortfall / ratePerSecond))
	}
	return Result{Allowed: allowed, Limit: int64(policy.RequestsPerMinute), Remaining: remaining, ResetSeconds: resetSeconds}, nil
}

func (e *Enforcer) fallback(isWrite bool, policy model.QuotaPolicy) (Result, error) {
	if isWrite {
		return Result{}, ErrQuotaBackendUnavailable
	}
	return Result{Allowed: true, Limit: int64(policy.RequestsPerMinute), Remaining: int64(policy.Burst), Degraded: true}, nil
}
