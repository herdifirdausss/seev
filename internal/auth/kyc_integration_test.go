//go:build integration

// Proves docs/roadmap/archive/39 Task T3's full KYC vertical against a real ledger and
// real Postgres, not mocks — including the exact gap a mock-only suite
// cannot catch: that ApproveKYCSubmission's applyTier callback is actually
// wired to a working ledger.Module.ApplyKycTier (docs/roadmap/archive/39 Task T5),
// which upserts REAL policy_limits rows. Reuses setupAuthTestDB from
// auth_integration_test.go (same package, same throwaway-container
// convention).
package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/auth"
	"github.com/herdifirdausss/seev/internal/kycvendor/mockkyc"
	"github.com/herdifirdausss/seev/internal/policy"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/shopspring/decimal"
)

func newAuthModuleWithMockKYC(db *database.DBSQL) (*auth.Module, *testutil.LedgerHarness) {
	ledgerModule := testutil.NewLedgerHarness(db)
	authModule := auth.NewModule(db, ledgerModule, auth.Config{
		JWTSecret: testJWTSecretIT, JWTIssuer: "seev-test",
		AccessExpiry: 15 * time.Minute, RefreshExpiry: 7 * 24 * time.Hour,
		DefaultCurrency: "IDR",
	}, nil, cryptoxTestRing, cryptoxTestLookup, mockkyc.New())
	return authModule, ledgerModule
}

func policyLimitMaxPerTxIT(t *testing.T, db *database.DBSQL, userID, txType string) int64 {
	t.Helper()
	var maxPerTx int64
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT max_per_tx FROM policy_limits WHERE user_id = $1 AND transaction_type = $2`, userID, txType).Scan(&maxPerTx))
	return maxPerTx
}

// TestAuth_KYC_L0ToL1_AutoApprove_AppliesRealLedgerTier proves the whole
// 0->1 vertical: SubmitKYC auto-approves (no mock_mode = approve), the
// user's kyc_level advances to 1 in seev_auth, AND the real ledger's
// policy_limits table gets the L1 template's caps for that specific user —
// the exact wiring that stayed broken (Unimplemented gRPC method) until
// docs/roadmap/archive/39 Task T5 was completed.
func TestAuth_KYC_L0ToL1_AutoApprove_AppliesRealLedgerTier(t *testing.T) {
	db := setupAuthTestDB(t)
	m, _ := newAuthModuleWithMockKYC(db)
	ctx := context.Background()

	u, _, err := m.Register(ctx, "kyc-l1@example.com", "hunter22!", "KYC One")
	require.NoError(t, err)

	submission, err := m.SubmitKYC(ctx, u.ID, 1, map[string]any{"name": "KYC One"})
	require.NoError(t, err)
	assert.Equal(t, "approved", submission.Status)

	status, err := m.KYC(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, status.Level)

	assert.Equal(t, int64(1_000_000), policyLimitMaxPerTxIT(t, db, u.ID.String(), "transfer_p2p"),
		"ApplyKycTier must have upserted the L1 template into the REAL ledger's policy_limits")
}

// TestAuth_KYC_L1ToL2_ReferThenAdminApprove_UpgradesRealLedgerTierInPlace
// proves the L2 path (always refer -> admin approves) end to end, and that
// upgrading overwrites the SAME policy_limits row rather than adding a
// second one.
func TestAuth_KYC_L1ToL2_ReferThenAdminApprove_UpgradesRealLedgerTierInPlace(t *testing.T) {
	db := setupAuthTestDB(t)
	m, _ := newAuthModuleWithMockKYC(db)
	ctx := context.Background()

	u, _, err := m.Register(ctx, "kyc-l2@example.com", "hunter22!", "KYC Two")
	require.NoError(t, err)
	_, err = m.SubmitKYC(ctx, u.ID, 1, nil)
	require.NoError(t, err)

	submission, err := m.SubmitKYC(ctx, u.ID, 2, map[string]any{"kyb_name": "Toko Maju"})
	require.NoError(t, err)
	assert.Equal(t, "pending", submission.Status, "L2 must always refer to manual review")

	require.NoError(t, m.ApproveKYC(ctx, submission.ID, "admin-1"))

	status, err := m.KYC(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, status.Level)
	assert.Equal(t, "approved", status.Submission.Status)

	assert.Equal(t, int64(100_000_000), policyLimitMaxPerTxIT(t, db, u.ID.String(), "transfer_p2p"),
		"upgrading L1->L2 must overwrite the SAME policy_limits row with L2's caps")
}

// TestAuth_KYC_Reject_LevelUnchangedNoLedgerCall proves a rejected
// submission never touches the ledger and the user's level stays put.
func TestAuth_KYC_Reject_LevelUnchangedNoLedgerCall(t *testing.T) {
	db := setupAuthTestDB(t)
	m, _ := newAuthModuleWithMockKYC(db)
	ctx := context.Background()

	u, _, err := m.Register(ctx, "kyc-reject@example.com", "hunter22!", "KYC Reject")
	require.NoError(t, err)

	submission, err := m.SubmitKYC(ctx, u.ID, 1, map[string]any{"mock_mode": mockkyc.ModeReject})
	require.NoError(t, err)
	assert.Equal(t, "rejected", submission.Status)

	status, err := m.KYC(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, status.Level)

	var rowCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM policy_limits WHERE user_id = $1`, u.ID).Scan(&rowCount))
	assert.Zero(t, rowCount, "a rejected submission must never create policy_limits rows")
}

// TestAuth_KYC_ApprovalSetsVerifiedUntil proves ApproveKYCSubmission writes
// a real kyc_verified_until deadline (migrations/auth/000018_kyc_expiry) —
// the gap the business-completeness audit found: KYC used to never expire.
func TestAuth_KYC_ApprovalSetsVerifiedUntil(t *testing.T) {
	db := setupAuthTestDB(t)
	ledgerModule := testutil.NewLedgerHarness(db)
	m := auth.NewModule(db, ledgerModule, auth.Config{
		JWTSecret: testJWTSecretIT, JWTIssuer: "seev-test",
		AccessExpiry: 15 * time.Minute, RefreshExpiry: 7 * 24 * time.Hour,
		DefaultCurrency: "IDR", KYCValidityTTL: 30 * 24 * time.Hour,
	}, nil, cryptoxTestRing, cryptoxTestLookup, mockkyc.New())
	ctx := context.Background()

	before := time.Now()
	u, _, err := m.Register(ctx, "kyc-verified-until@example.com", "hunter22!", "KYC VerifiedUntil")
	require.NoError(t, err)
	_, err = m.SubmitKYC(ctx, u.ID, 1, map[string]any{"name": "KYC VerifiedUntil"})
	require.NoError(t, err)

	status, err := m.KYC(ctx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, status.VerifiedUntil, "an approved level must carry a validity deadline")
	assert.WithinDuration(t, before.Add(30*24*time.Hour), *status.VerifiedUntil, time.Minute)
}

// TestAuth_KYC_ExpiredLevel_DowngradedByExpiryWorker proves the periodic
// expiry worker (internal/auth/worker/expiry.go) closes the loop: an
// expired level gets downgraded to L0, kyc_verified_until clears, and the
// REAL ledger's policy_limits drop back to L0's caps — reusing DowngradeKYC's
// existing limits-first path end to end, not just a unit-level fake.
func TestAuth_KYC_ExpiredLevel_DowngradedByExpiryWorker(t *testing.T) {
	db := setupAuthTestDB(t)
	ledgerModule := testutil.NewLedgerHarness(db)
	m := auth.NewModule(db, ledgerModule, auth.Config{
		JWTSecret: testJWTSecretIT, JWTIssuer: "seev-test",
		AccessExpiry: 15 * time.Minute, RefreshExpiry: 7 * 24 * time.Hour,
		DefaultCurrency: "IDR", KYCValidityTTL: -time.Hour, // already expired at approval time
	}, nil, cryptoxTestRing, cryptoxTestLookup, mockkyc.New())
	ctx := context.Background()

	u, _, err := m.Register(ctx, "kyc-expired@example.com", "hunter22!", "KYC Expired")
	require.NoError(t, err)
	submission, err := m.SubmitKYC(ctx, u.ID, 1, map[string]any{"name": "KYC Expired"})
	require.NoError(t, err)
	require.Equal(t, "approved", submission.Status)
	require.Equal(t, int64(1_000_000), policyLimitMaxPerTxIT(t, db, u.ID.String(), "transfer_p2p"))

	job := m.NewKYCExpiryJob(nil, time.Hour, nil)
	require.NoError(t, job.RunOnce(ctx))

	status, err := m.KYC(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, status.Level, "expiry worker must downgrade back to L0")
	assert.Nil(t, status.VerifiedUntil, "downgrade clears the validity deadline")
	assert.Equal(t, int64(0), policyLimitMaxPerTxIT(t, db, u.ID.String(), "transfer_p2p"),
		"the real ledger's policy_limits must be re-materialized to L0")

	// Second pass is a no-op: the user is no longer above L0.
	require.NoError(t, job.RunOnce(ctx))
	status, err = m.KYC(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, status.Level)
}

// TestAuth_KYC_NotYetExpired_UntouchedByExpiryWorker proves the worker only
// acts on genuinely expired rows.
func TestAuth_KYC_NotYetExpired_UntouchedByExpiryWorker(t *testing.T) {
	db := setupAuthTestDB(t)
	m, _ := newAuthModuleWithMockKYC(db)
	ctx := context.Background()

	u, _, err := m.Register(ctx, "kyc-not-expired@example.com", "hunter22!", "KYC NotExpired")
	require.NoError(t, err)
	_, err = m.SubmitKYC(ctx, u.ID, 1, nil)
	require.NoError(t, err)

	job := m.NewKYCExpiryJob(nil, time.Hour, nil)
	require.NoError(t, job.RunOnce(ctx))

	status, err := m.KYC(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, status.Level, "a level well within its validity window must not be downgraded")
}

func TestAuth_KYC_DowngradeL0_HardPolicyBeatsStaleToken(t *testing.T) {
	db := setupAuthTestDB(t)
	m, _ := newAuthModuleWithMockKYC(db)
	ctx := context.Background()

	u, pair, err := m.Register(ctx, "kyc-downgrade@example.com", "hunter22!", "KYC Downgrade")
	require.NoError(t, err)
	_, err = m.SubmitKYC(ctx, u.ID, 1, nil)
	require.NoError(t, err)
	oldClaims, err := middleware.ParseToken(testJWTSecretIT, pair.AccessToken, "seev-test")
	require.NoError(t, err)
	assert.Equal(t, 0, oldClaims.KYCLevel, "the registration token predates approval; refresh is intentionally separate")

	// Mint the stale L1 token that a client could still hold at downgrade time.
	stale, err := middleware.GenerateToken(testJWTSecretIT, middleware.Claims{
		UserID: u.ID.String(), Role: "user", KYCLevel: 1, Exp: time.Now().Add(time.Hour).Unix(), Iss: "seev-test",
	})
	require.NoError(t, err)
	staleClaims, err := middleware.ParseToken(testJWTSecretIT, stale, "seev-test")
	require.NoError(t, err)
	assert.Equal(t, 1, staleClaims.KYCLevel)

	require.NoError(t, m.DowngradeKYC(ctx, u.ID, 0, "admin-1", "manual review"))
	assert.Equal(t, int64(0), policyLimitMaxPerTxIT(t, db, u.ID.String(), "transfer_p2p"))

	engine := policy.New(policy.NewRepository(db), cache.NewMemoryCounter(), time.UTC, nil)
	allowed, rule, _, err := engine.Check(ctx, u.ID, "transfer_p2p", decimal.NewFromInt(1))
	require.NoError(t, err)
	assert.False(t, allowed, "L0 hard limits must reject even while a stale L1 token exists")
	assert.Equal(t, "max_per_tx", rule)
}
