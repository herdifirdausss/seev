package quota

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
)

type fakeQuotaRepo struct {
	byKey map[string]model.QuotaPolicy
}

func newFakeQuotaRepo() *fakeQuotaRepo { return &fakeQuotaRepo{byKey: map[string]model.QuotaPolicy{}} }

func (f *fakeQuotaRepo) Upsert(_ context.Context, p model.QuotaPolicy) error {
	f.byKey[p.TenantID.String()+"|"+p.QuotaClass] = p
	return nil
}

func (f *fakeQuotaRepo) GetByTenantAndClass(_ context.Context, tenantID uuid.UUID, quotaClass string) (model.QuotaPolicy, error) {
	p, ok := f.byKey[tenantID.String()+"|"+quotaClass]
	if !ok {
		return model.QuotaPolicy{}, repository.ErrNotFound
	}
	return p, nil
}

func newMiniredisClient(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

func TestEnforcer_Check_AllowsWithinBurstThenRejects(t *testing.T) {
	repo := newFakeQuotaRepo()
	tenantID := uuid.New()
	require.NoError(t, repo.Upsert(context.Background(), model.QuotaPolicy{
		TenantID: tenantID, QuotaClass: "transfers", RequestsPerMinute: 60, Burst: 2, IsEnabled: true,
	}))
	enforcer := NewEnforcer(repo, newMiniredisClient(t))

	r1, err := enforcer.Check(context.Background(), tenantID, "transfers", true)
	require.NoError(t, err)
	assert.True(t, r1.Allowed)

	r2, err := enforcer.Check(context.Background(), tenantID, "transfers", true)
	require.NoError(t, err)
	assert.True(t, r2.Allowed)

	r3, err := enforcer.Check(context.Background(), tenantID, "transfers", true)
	require.NoError(t, err)
	assert.False(t, r3.Allowed, "the third request must exceed a burst of 2")
	assert.Positive(t, r3.ResetSeconds)
}

func TestEnforcer_Check_TenantsAreIndependent(t *testing.T) {
	repo := newFakeQuotaRepo()
	tenantA, tenantB := uuid.New(), uuid.New()
	for _, tid := range []uuid.UUID{tenantA, tenantB} {
		require.NoError(t, repo.Upsert(context.Background(), model.QuotaPolicy{
			TenantID: tid, QuotaClass: "transfers", RequestsPerMinute: 60, Burst: 1, IsEnabled: true,
		}))
	}
	enforcer := NewEnforcer(repo, newMiniredisClient(t))

	rA, err := enforcer.Check(context.Background(), tenantA, "transfers", true)
	require.NoError(t, err)
	assert.True(t, rA.Allowed)

	// Exhaust tenant A's burst.
	rA2, err := enforcer.Check(context.Background(), tenantA, "transfers", true)
	require.NoError(t, err)
	assert.False(t, rA2.Allowed)

	// Tenant B is unaffected.
	rB, err := enforcer.Check(context.Background(), tenantB, "transfers", true)
	require.NoError(t, err)
	assert.True(t, rB.Allowed, "one tenant exhausting its quota must never affect another tenant")
}

func TestEnforcer_Check_NoPolicyRow_UsesSecureDefault(t *testing.T) {
	repo := newFakeQuotaRepo()
	enforcer := NewEnforcer(repo, newMiniredisClient(t))
	result, err := enforcer.Check(context.Background(), uuid.New(), "unconfigured-class", true)
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.Equal(t, int64(defaultPolicy.RequestsPerMinute), result.Limit)
}

func TestEnforcer_Check_DisabledPolicy_AlwaysAllows(t *testing.T) {
	repo := newFakeQuotaRepo()
	tenantID := uuid.New()
	require.NoError(t, repo.Upsert(context.Background(), model.QuotaPolicy{
		TenantID: tenantID, QuotaClass: "transfers", RequestsPerMinute: 1, Burst: 1, IsEnabled: false,
	}))
	enforcer := NewEnforcer(repo, newMiniredisClient(t))

	for range 5 {
		result, err := enforcer.Check(context.Background(), tenantID, "transfers", true)
		require.NoError(t, err)
		assert.True(t, result.Allowed, "a disabled policy must never block requests")
	}
}

// TestEnforcer_Check_RedisUnavailable_WriteFailsClosed proves T4's own
// required posture: "Redis outage blocks financial writes."
func TestEnforcer_Check_RedisUnavailable_WriteFailsClosed(t *testing.T) {
	repo := newFakeQuotaRepo()
	tenantID := uuid.New()
	require.NoError(t, repo.Upsert(context.Background(), model.QuotaPolicy{
		TenantID: tenantID, QuotaClass: "transfers", RequestsPerMinute: 60, Burst: 60, IsEnabled: true,
	}))

	// A nil *redis.Client is the simplest deterministic stand-in for
	// "Redis unreachable" — Check must never silently allow a write.
	enforcer := NewEnforcer(repo, nil)
	_, err := enforcer.Check(context.Background(), tenantID, "transfers", true)
	assert.ErrorIs(t, err, ErrQuotaBackendUnavailable)
}

// TestEnforcer_Check_RedisUnavailable_ReadDegradedAllow proves the
// bounded, OBSERVABLE read fallback (§4) — reads are allowed, but the
// result is explicitly marked Degraded so callers can alert on it (T9),
// not silently treated as a normal green check.
func TestEnforcer_Check_RedisUnavailable_ReadDegradedAllow(t *testing.T) {
	repo := newFakeQuotaRepo()
	tenantID := uuid.New()
	require.NoError(t, repo.Upsert(context.Background(), model.QuotaPolicy{
		TenantID: tenantID, QuotaClass: "reads", RequestsPerMinute: 60, Burst: 60, IsEnabled: true,
	}))

	enforcer := NewEnforcer(repo, nil)
	result, err := enforcer.Check(context.Background(), tenantID, "reads", false)
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.True(t, result.Degraded, "a read fallback must be explicitly marked degraded, not indistinguishable from a normal allow")
}

func TestEnforcer_Check_RedisConnectionRefused_TreatedAsUnavailable(t *testing.T) {
	repo := newFakeQuotaRepo()
	tenantID := uuid.New()
	require.NoError(t, repo.Upsert(context.Background(), model.QuotaPolicy{
		TenantID: tenantID, QuotaClass: "transfers", RequestsPerMinute: 60, Burst: 60, IsEnabled: true,
	}))

	// A real *redis.Client pointed at a closed port — proves the bounded
	// redisPingTimeout actually triggers the fallback path rather than
	// hanging or returning a raw driver error to the caller.
	deadClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
	defer deadClient.Close()
	enforcer := NewEnforcer(repo, deadClient)

	_, err := enforcer.Check(context.Background(), tenantID, "transfers", true)
	assert.True(t, errors.Is(err, ErrQuotaBackendUnavailable), "an unreachable Redis must be treated identically to a nil client")
}
