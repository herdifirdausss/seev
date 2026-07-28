//go:build integration

package payout_test

// Proves Plan 57 T6's payout acceptance criteria end to end against a
// real ledger and Postgres, reusing this package's own
// setupPayoutTestDB/newPayoutTestModules/getAccountBalance/
// assertLedgerBalanced helpers (payout_integration_test.go, same
// package): a merchant payout holds/debits/releases the correct merchant
// account exactly once, a lost synchronous vendor response is recoverable
// via the SAME idempotent ResumeStuck/Query path the user journey already
// relies on, and sandbox routing is structural.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/internal/vendorgw/mockvendor"
	"github.com/herdifirdausss/seev/pkg/database"
)

func merchantCashAccountID(t *testing.T, ledgerModule *testutil.LedgerHarness, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	acc, err := ledgerModule.Module().ProvisionMerchant(context.Background(), tenantID, "IDR")
	require.NoError(t, err)
	return acc.ID
}

func TestPayout_CreateMerchant_InstantSettle_HoldsDebitsReleasesOnce(t *testing.T) {
	db := setupPayoutTestDB(t)
	ledgerModule, payoutModule, _ := newPayoutTestModules(db)
	ctx := context.Background()

	tenantID := uuid.New()
	cash := merchantCashAccountID(t, ledgerModule, tenantID)
	seedMerchantCash(t, db, cash, 200_000)

	id, err := payoutModule.CreateMerchant(ctx, tenantID, "live", "IDR", decimal.NewFromInt(100_000), mockDestination(""), "test")
	require.NoError(t, err)

	req, err := payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSubmitted, req.Status, "CreateMerchant must return before any vendor result — dispatch is async")
	assert.Equal(t, tenantID, req.MerchantTenantID)

	n, err := payoutModule.DispatchPendingCommands(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	req, err = payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, mockvendor.VendorName, req.Vendor, "the seeded fallback routing rule must still resolve mockvendor for a merchant's wildcard live request")
	assert.Equal(t, model.StatusSettled, req.Status)
	require.NotNil(t, req.HoldTxID)
	require.NotNil(t, req.SettleTxID)

	assert.True(t, getAccountBalance(t, db, cash).Equal(decimal.NewFromInt(100_000)),
		"the merchant's own cash account must drop by exactly the settled payout amount, once")

	assertLedgerBalanced(t, db)
}

// TestPayout_CreateMerchant_Async_ResumeJobSettles proves T6's own
// "lost synchronous response is recoverable by idempotent query/retry"
// acceptance criterion for the merchant path — the EXACT SAME
// ResumeStuck/Query recovery mechanism the user journey already relies
// on (TestPayout_Create_Async_ResumeJobSettles), unmodified, now driving
// a merchant_payout_settle instead of withdraw_settle.
func TestPayout_CreateMerchant_Async_ResumeJobSettles(t *testing.T) {
	db := setupPayoutTestDB(t)
	ledgerModule, payoutModule, provider := newPayoutTestModules(db)
	ctx := context.Background()

	tenantID := uuid.New()
	cash := merchantCashAccountID(t, ledgerModule, tenantID)
	seedMerchantCash(t, db, cash, 200_000)

	id, err := payoutModule.CreateMerchant(ctx, tenantID, "live", "IDR", decimal.NewFromInt(75_000), mockDestination(mockvendor.ModeAsync), "test")
	require.NoError(t, err)

	n, err := payoutModule.DispatchPendingCommands(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	req, err := payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusVendorPending, req.Status, "an async submit result must leave the request pending, not terminal")
	assert.True(t, getAccountBalance(t, db, cash).Equal(decimal.NewFromInt(125_000)), "the hold amount must already be out of cash even though the vendor hasn't settled yet")

	// Simulate the vendor resolving the payout out of band (the
	// "lost synchronous response" scenario), then let the resume job
	// recover it via Query — never a fresh Submit.
	provider.CompletePending(id.String(), vendorgw.PayoutSettled, "")
	resumed, failed, err := payoutModule.ResumeStuck(ctx, -time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, resumed)
	assert.Equal(t, 0, failed)

	req, err = payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSettled, req.Status)
	assert.True(t, getAccountBalance(t, db, cash).Equal(decimal.NewFromInt(125_000)), "balance must not change again on settle — the hold already left cash")
	assert.Equal(t, 1, vendorCallCount(t, db, id, "submit"), "the original CreateMerchant call must be the only Submit")
	assert.Equal(t, 1, vendorCallCount(t, db, id, "query"), "recovery must Query, never re-Submit, a vendor_pending request")

	assertLedgerBalanced(t, db)
}

// TestPayout_CreateMerchant_VendorFails_MoneyReturnedToMerchantCash
// proves the release side of "holds/debits/releases the correct merchant
// account once" — a synchronous vendor rejection cancels the hold and
// the FULL amount comes back to the tenant's own cash account.
func TestPayout_CreateMerchant_VendorFails_MoneyReturnedToMerchantCash(t *testing.T) {
	db := setupPayoutTestDB(t)
	ledgerModule, payoutModule, _ := newPayoutTestModules(db)
	ctx := context.Background()

	tenantID := uuid.New()
	cash := merchantCashAccountID(t, ledgerModule, tenantID)
	seedMerchantCash(t, db, cash, 200_000)

	id, err := payoutModule.CreateMerchant(ctx, tenantID, "live", "IDR", decimal.NewFromInt(50_000), mockDestination(mockvendor.ModeFail), "test")
	require.NoError(t, err)

	_, err = payoutModule.DispatchPendingCommands(ctx, 10)
	require.NoError(t, err)

	req, err := payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCancelled, req.Status)

	assert.True(t, getAccountBalance(t, db, cash).Equal(decimal.NewFromInt(200_000)),
		"a cancelled merchant payout must return the FULL held amount to the tenant's cash account")

	assertLedgerBalanced(t, db)
}

func TestPayout_CreateMerchant_Sandbox_AlwaysRoutesToMockVendor(t *testing.T) {
	db := setupPayoutTestDB(t)
	ledgerModule, payoutModule, _ := newPayoutTestModules(db)
	ctx := context.Background()

	tenantID := uuid.New()
	cash := merchantCashAccountID(t, ledgerModule, tenantID)
	seedMerchantCash(t, db, cash, 100_000)

	id, err := payoutModule.CreateMerchant(ctx, tenantID, "sandbox", "IDR", decimal.NewFromInt(40_000), mockDestination(""), "test")
	require.NoError(t, err)

	_, err = payoutModule.DispatchPendingCommands(ctx, 10)
	require.NoError(t, err)

	req, err := payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, mockvendor.VendorName, req.Vendor)
	assert.Equal(t, model.StatusSettled, req.Status)
}

// seedMerchantCash directly credits a merchant account for test setup —
// T6 deliberately does not add a merchant-to-merchant funding path here;
// this mirrors internal/ledger's own schema_contract_test.go established
// direct-SQL balance seeding for test preconditions (same pattern T5's
// own merchant_transfer_integration_test.go uses).
func seedMerchantCash(t *testing.T, db *database.DBSQL, accountID uuid.UUID, amount int64) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`UPDATE account_balances SET balance = balance + $1 WHERE account_id = $2`, amount, accountID)
	require.NoError(t, err)
}
