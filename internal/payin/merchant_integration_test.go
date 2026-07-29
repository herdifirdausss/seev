//go:build integration

package payin_test

// Proves Plan 57 T6's payin acceptance criteria end to end against a real
// ledger and Postgres, reusing this package's own setupPayinTestDB/
// newPayinModule/getBalance helpers (payin_integration_test.go, same
// package): pay-in credits the correct merchant account exactly once,
// a duplicate callback is safe (no double-credit), and sandbox routing
// is structural (never falls through to a live-only vendor).

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/payin"
	"github.com/herdifirdausss/seev/internal/testutil"
)

// merchantCashAccountID provisions tenantID's ledger accounts (Plan 57
// T5's ProvisionMerchant, which also provisions the T6 hold account) and
// returns the cash account id.
func merchantCashAccountID(t *testing.T, ledgerModule *testutil.LedgerHarness, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	acc, err := ledgerModule.Module().ProvisionMerchant(context.Background(), tenantID, "IDR")
	require.NoError(t, err)
	return acc.ID
}

func TestPayin_MerchantTopup_CreditsCorrectMerchantAccountOnce(t *testing.T) {
	db := setupPayinTestDB(t)
	m := newPayinModule(db)
	ctx := context.Background()

	ledgerModule := testutil.NewLedgerHarness(db)
	tenantID := uuid.New()
	cash := merchantCashAccountID(t, ledgerModule, tenantID)

	intent, err := m.CreateMerchantTopupIntent(ctx, tenantID, "live", "IDR", decimal.NewFromInt(300_000), "downstream-key-1")
	require.NoError(t, err)
	require.Equal(t, "mockvendor", intent.Vendor, "the seeded fallback routing rule must still resolve mockvendor for a merchant's wildcard live request")

	_, err = m.HandleVendorCallback(ctx, intent.Vendor, "evt-merchant-1", intent.Reference, "300000", "IDR", "settled", "2026-07-13T00:00:00Z", "inbox-1", "req-1", "")
	require.NoError(t, err)

	assert.True(t, getBalance(t, db, cash).Equal(decimal.NewFromInt(300_000)), "the merchant's own cash account must be credited exactly once")

	var eventCount, txCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM payin_webhook_events WHERE vendor_event_id = 'evt-merchant-1'`).Scan(&eventCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_transactions WHERE idempotency_key = 'payin:mockvendor:evt-merchant-1' AND type = 'merchant_payin_credit'`).Scan(&txCount))
	assert.Equal(t, 1, eventCount)
	assert.Equal(t, 1, txCount)
}

// TestPayin_MerchantTopup_DuplicateCallbackIsSafe proves T6's "duplicate
// synchronous request and duplicate callback are safe" for the pay-in
// side — the exact same redelivery pattern
// TestPayin_NormalizedCallback_IsIdempotent already proves for the user
// path, run here against a merchant-owned intent.
func TestPayin_MerchantTopup_DuplicateCallbackIsSafe(t *testing.T) {
	db := setupPayinTestDB(t)
	m := newPayinModule(db)
	ctx := context.Background()

	ledgerModule := testutil.NewLedgerHarness(db)
	tenantID := uuid.New()
	cash := merchantCashAccountID(t, ledgerModule, tenantID)

	intent, err := m.CreateMerchantTopupIntent(ctx, tenantID, "live", "IDR", decimal.NewFromInt(150_000), "downstream-key-2")
	require.NoError(t, err)

	call := func() error {
		_, callErr := m.HandleVendorCallback(ctx, intent.Vendor, "evt-merchant-dup", intent.Reference, "150000", "IDR", "settled", "2026-07-13T00:00:00Z", "inbox-1", "req-1", "")
		return callErr
	}
	require.NoError(t, call())
	require.NoError(t, call()) // redelivery — must not double-credit

	assert.True(t, getBalance(t, db, cash).Equal(decimal.NewFromInt(150_000)), "a redelivered callback must never credit the merchant account twice")

	var txCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_transactions WHERE idempotency_key = 'payin:mockvendor:evt-merchant-dup'`).Scan(&txCount))
	assert.Equal(t, 1, txCount)
}

// TestPayin_MerchantTopup_Sandbox_AlwaysRoutesToMockVendor proves the
// structural sandbox-to-mock enforcement end to end: a sandbox tenant's
// intent is routed to mockvendor even though no explicit routing rule
// names it for this request, and the full settle flow still credits the
// tenant's account correctly.
func TestPayin_MerchantTopup_Sandbox_AlwaysRoutesToMockVendor(t *testing.T) {
	db := setupPayinTestDB(t)
	m := newPayinModule(db)
	ctx := context.Background()

	ledgerModule := testutil.NewLedgerHarness(db)
	tenantID := uuid.New()
	cash := merchantCashAccountID(t, ledgerModule, tenantID)

	intent, err := m.CreateMerchantTopupIntent(ctx, tenantID, "sandbox", "IDR", decimal.NewFromInt(75_000), "downstream-key-3")
	require.NoError(t, err)
	assert.Equal(t, "mockvendor", intent.Vendor)

	_, err = m.HandleVendorCallback(ctx, intent.Vendor, "evt-merchant-sandbox", intent.Reference, "75000", "IDR", "settled", "2026-07-13T00:00:00Z", "inbox-1", "req-1", "")
	require.NoError(t, err)
	assert.True(t, getBalance(t, db, cash).Equal(decimal.NewFromInt(75_000)))
}

// TestPayin_CreateMerchantTopupIntent_DownstreamKeyConflict_NoSecondRow
// proves the real Postgres partial unique index backs the "Gateway crash
// after owner-service success" recovery path (docs/reference/c1-b2b-design.md
// §10.4): calling CreateMerchantTopupIntent twice with the SAME
// downstreamKey (simulating a Gateway retry) must return the identical
// intent and never insert a second row.
func TestPayin_CreateMerchantTopupIntent_DownstreamKeyConflict_NoSecondRow(t *testing.T) {
	db := setupPayinTestDB(t)
	m := newPayinModule(db)
	ctx := context.Background()

	ledgerModule := testutil.NewLedgerHarness(db)
	tenantID := uuid.New()
	merchantCashAccountID(t, ledgerModule, tenantID)

	first, err := m.CreateMerchantTopupIntent(ctx, tenantID, "live", "IDR", decimal.NewFromInt(200_000), "shared-downstream-key")
	require.NoError(t, err)

	second, err := m.CreateMerchantTopupIntent(ctx, tenantID, "live", "IDR", decimal.NewFromInt(200_000), "shared-downstream-key")
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID, "a retry with the same downstream key must recover the original intent, not create a new one")

	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM payin_topup_intents WHERE merchant_tenant_id = $1`, tenantID).Scan(&count))
	assert.Equal(t, 1, count, "exactly one row must exist for this downstream key, regardless of retry count")
}

// TestPayin_GetMerchantTopupIntent_CrossTenant_NotFound proves §7.3's
// tenant-isolation rule: a topup intent owned by tenant A is invisible to
// tenant B, indistinguishable from a genuinely missing id (§6.7).
func TestPayin_GetMerchantTopupIntent_CrossTenant_NotFound(t *testing.T) {
	db := setupPayinTestDB(t)
	m := newPayinModule(db)
	ctx := context.Background()

	ledgerModule := testutil.NewLedgerHarness(db)
	tenantA := uuid.New()
	tenantB := uuid.New()
	merchantCashAccountID(t, ledgerModule, tenantA)
	merchantCashAccountID(t, ledgerModule, tenantB)

	intent, err := m.CreateMerchantTopupIntent(ctx, tenantA, "live", "IDR", decimal.NewFromInt(100_000), "tenant-a-key")
	require.NoError(t, err)

	_, err = m.GetMerchantTopupIntent(ctx, tenantB, intent.ID)
	assert.ErrorIs(t, err, payin.ErrTopupIntentNotFound)

	found, err := m.GetMerchantTopupIntent(ctx, tenantA, intent.ID)
	require.NoError(t, err)
	assert.Equal(t, intent.ID, found.ID)
}
