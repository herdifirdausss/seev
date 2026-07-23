package policy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/pkg/cache"
)

// failingCounter always errors — used to prove Check fails OPEN on an
// infra error (docs/roadmap/archive/17 Task T1, chaos-tested convention).
type failingCounter struct{}

func (failingCounter) IncrBy(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, errors.New("counter unavailable")
}
func (failingCounter) Get(context.Context, string) (int64, error) {
	return 0, errors.New("counter unavailable")
}

func int64Ptr(v int64) *int64 { return &v }
func int32Ptr(v int32) *int32 { return &v }

func newEngine(t *testing.T, repo Repository) (*Engine, *cache.MemoryCounter) {
	t.Helper()
	counter := cache.NewMemoryCounter()
	t.Cleanup(counter.Stop)
	loc, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)
	return New(repo, counter, loc, nil), counter
}

// ─── Check: no limit configured ────────────────────────────────────────────

func TestCheck_NoLimitRow_Allowed(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	repo.EXPECT().GetEffective(gomock.Any(), gomock.Any(), "transfer_p2p").Return(Limit{}, false, nil)

	e, _ := newEngine(t, repo)
	allowed, rule, _, err := e.Check(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(1000))

	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Empty(t, rule)
}

func TestCheck_DisabledLimit_Allowed(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	repo.EXPECT().GetEffective(gomock.Any(), gomock.Any(), "transfer_p2p").Return(Limit{
		TransactionType: "transfer_p2p", MaxPerTx: int64Ptr(100), Enabled: false,
	}, true, nil)

	e, _ := newEngine(t, repo)
	allowed, _, _, err := e.Check(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(999999))

	require.NoError(t, err)
	assert.True(t, allowed, "a disabled limit row must never reject")
}

// ─── Check: max_per_tx ──────────────────────────────────────────────────────

func TestCheck_MaxPerTx_WithinLimit_Allowed(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	repo.EXPECT().GetEffective(gomock.Any(), gomock.Any(), "transfer_p2p").Return(Limit{
		TransactionType: "transfer_p2p", MaxPerTx: int64Ptr(10000), Enabled: true,
	}, true, nil)

	e, _ := newEngine(t, repo)
	allowed, _, _, err := e.Check(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(10000))

	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestCheck_MaxPerTx_Exceeded_Rejected(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	repo.EXPECT().GetEffective(gomock.Any(), gomock.Any(), "transfer_p2p").Return(Limit{
		TransactionType: "transfer_p2p", MaxPerTx: int64Ptr(10000), Enabled: true,
	}, true, nil)

	e, _ := newEngine(t, repo)
	allowed, rule, detail, err := e.Check(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(10001))

	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Equal(t, "max_per_tx", rule)
	assert.NotEmpty(t, detail)
}

// ─── Check: velocity (daily amount / count, monthly amount) ────────────────

func TestCheck_MaxDailyAmount_AccountsForPriorUsage(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	userID := uuid.New()
	repo.EXPECT().GetEffective(gomock.Any(), userID, "transfer_p2p").Return(Limit{
		TransactionType: "transfer_p2p", MaxDailyAmount: int64Ptr(10000), Enabled: true,
	}, true, nil).AnyTimes()

	e, counter := newEngine(t, repo)
	ctx := context.Background()
	now := time.Now().In(e.loc)

	// Simulate 8000 already used today.
	_, err := counter.IncrBy(ctx, DailyAmountKey(userID, "transfer_p2p", now), 8000, 48*time.Hour)
	require.NoError(t, err)

	allowed, _, _, err := e.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(2000))
	require.NoError(t, err)
	assert.True(t, allowed, "8000 + 2000 == 10000 must be allowed (at the limit, not over)")

	allowed, rule, _, err := e.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(2001))
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Equal(t, "max_daily_amount", rule)
}

func TestCheck_MaxDailyCount_AccountsForPriorUsage(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	userID := uuid.New()
	repo.EXPECT().GetEffective(gomock.Any(), userID, "withdraw_initiate").Return(Limit{
		TransactionType: "withdraw_initiate", MaxDailyCount: int32Ptr(3), Enabled: true,
	}, true, nil).AnyTimes()

	e, counter := newEngine(t, repo)
	ctx := context.Background()
	now := time.Now().In(e.loc)

	_, err := counter.IncrBy(ctx, DailyCountKey(userID, "withdraw_initiate", now), 3, 48*time.Hour)
	require.NoError(t, err)

	allowed, rule, _, err := e.Check(ctx, userID, "withdraw_initiate", decimal.NewFromInt(100))
	require.NoError(t, err)
	assert.False(t, allowed, "3 already used, limit 3 — a 4th must be rejected")
	assert.Equal(t, "max_daily_count", rule)
}

func TestCheck_MaxMonthlyAmount_AccountsForPriorUsage(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	userID := uuid.New()
	repo.EXPECT().GetEffective(gomock.Any(), userID, "transfer_p2p").Return(Limit{
		TransactionType: "transfer_p2p", MaxMonthlyAmount: int64Ptr(100000), Enabled: true,
	}, true, nil).AnyTimes()

	e, counter := newEngine(t, repo)
	ctx := context.Background()
	now := time.Now().In(e.loc)

	_, err := counter.IncrBy(ctx, MonthlyAmountKey(userID, "transfer_p2p", now), 99000, 35*24*time.Hour)
	require.NoError(t, err)

	allowed, rule, _, err := e.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(1001))
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Equal(t, "max_monthly_amount", rule)
}

// ─── Check: user override wins over type-wide default ─────────────────────

func TestCheck_UserOverride_TakesPrecedenceOverDefault(t *testing.T) {
	// This is GetEffective's own contract (repository.go), exercised here
	// via the mock returning what the user-specific row would produce —
	// Engine itself has no override-selection logic, it trusts the
	// repository's single query to have already resolved precedence.
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	userID := uuid.New()
	repo.EXPECT().GetEffective(gomock.Any(), userID, "transfer_p2p").Return(Limit{
		UserID: &userID, TransactionType: "transfer_p2p", MaxPerTx: int64Ptr(500), Enabled: true,
	}, true, nil)

	e, _ := newEngine(t, repo)
	allowed, rule, _, err := e.Check(context.Background(), userID, "transfer_p2p", decimal.NewFromInt(600))

	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Equal(t, "max_per_tx", rule)
}

// ─── getLimit: in-process cache ────────────────────────────────────────────

func TestGetLimit_CachesWithinTTL(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	userID := uuid.New()
	// Exactly ONE call expected — a second Check within the TTL window must
	// be served from cache, not hit the repository again.
	repo.EXPECT().GetEffective(gomock.Any(), userID, "transfer_p2p").Return(Limit{
		TransactionType: "transfer_p2p", MaxPerTx: int64Ptr(100), Enabled: true,
	}, true, nil).Times(1)

	counter := cache.NewMemoryCounter()
	defer counter.Stop()
	e := New(repo, counter, time.UTC, nil, WithCacheTTL(time.Minute))

	_, _, _, err := e.Check(context.Background(), userID, "transfer_p2p", decimal.NewFromInt(50))
	require.NoError(t, err)
	_, _, _, err = e.Check(context.Background(), userID, "transfer_p2p", decimal.NewFromInt(50))
	require.NoError(t, err)
}

func TestGetLimit_RefetchesAfterTTLExpires(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	userID := uuid.New()
	// TWO calls expected — the cache TTL is tiny, so the second Check must
	// miss the cache and hit the repository again.
	repo.EXPECT().GetEffective(gomock.Any(), userID, "transfer_p2p").Return(Limit{
		TransactionType: "transfer_p2p", MaxPerTx: int64Ptr(100), Enabled: true,
	}, true, nil).Times(2)

	counter := cache.NewMemoryCounter()
	defer counter.Stop()
	e := New(repo, counter, time.UTC, nil, WithCacheTTL(10*time.Millisecond))

	_, _, _, err := e.Check(context.Background(), userID, "transfer_p2p", decimal.NewFromInt(50))
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)

	_, _, _, err = e.Check(context.Background(), userID, "transfer_p2p", decimal.NewFromInt(50))
	require.NoError(t, err)
}

// ─── Record ──────────────────────────────────────────────────────────────

func TestRecord_IncrementsAllThreeCounters(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	e, counter := newEngine(t, repo)
	ctx := context.Background()
	userID := uuid.New()
	now := time.Now().In(e.loc)

	e.Record(ctx, userID, "transfer_p2p", decimal.NewFromInt(500))

	dailyAmt, err := counter.Get(ctx, DailyAmountKey(userID, "transfer_p2p", now))
	require.NoError(t, err)
	assert.Equal(t, int64(500), dailyAmt)

	dailyCnt, err := counter.Get(ctx, DailyCountKey(userID, "transfer_p2p", now))
	require.NoError(t, err)
	assert.Equal(t, int64(1), dailyCnt)

	monthlyAmt, err := counter.Get(ctx, MonthlyAmountKey(userID, "transfer_p2p", now))
	require.NoError(t, err)
	assert.Equal(t, int64(500), monthlyAmt)
}

// ─── Check: fails OPEN on infrastructure errors ────────────────────────────

func TestCheck_RepositoryError_FailsOpen(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	repo.EXPECT().GetEffective(gomock.Any(), gomock.Any(), "transfer_p2p").
		Return(Limit{}, false, errors.New("db unreachable"))

	e, _ := newEngine(t, repo)
	allowed, rule, _, err := e.Check(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(999999))

	require.NoError(t, err, "Check must never surface an infra error to the caller — it fails open instead")
	assert.True(t, allowed)
	assert.Empty(t, rule)
}

func TestCheck_CounterError_FailsOpen(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	repo.EXPECT().GetEffective(gomock.Any(), gomock.Any(), "transfer_p2p").Return(Limit{
		TransactionType: "transfer_p2p", MaxDailyAmount: int64Ptr(1000), Enabled: true,
	}, true, nil)

	loc, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)
	e := New(repo, failingCounter{}, loc, nil)

	allowed, rule, _, err := e.Check(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(999999))
	require.NoError(t, err, "a counter (Redis) outage must fail open, not 500 every request of this type")
	assert.True(t, allowed)
	assert.Empty(t, rule)
}

func TestCheck_MaxPerTxAlone_NeverTouchesCounter(t *testing.T) {
	// max_per_tx is a pure arithmetic check against the request amount —
	// it must not depend on the counter at all, so a broken counter can't
	// affect it either way.
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	repo.EXPECT().GetEffective(gomock.Any(), gomock.Any(), "transfer_p2p").Return(Limit{
		TransactionType: "transfer_p2p", MaxPerTx: int64Ptr(100), Enabled: true,
	}, true, nil)

	loc, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)
	e := New(repo, failingCounter{}, loc, nil)

	allowed, rule, _, err := e.Check(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(200))
	require.NoError(t, err)
	assert.False(t, allowed, "max_per_tx must still be enforced even with a broken counter")
	assert.Equal(t, "max_per_tx", rule)
}
