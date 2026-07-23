//go:build integration

// Package payout_test drives internal/payout.Module.Create end to end
// against a real ledger.Module and real Postgres (docs/roadmap/archive/23 Task T3's
// required test: "the user balance decreases on hold, funds move correctly on
// settle, and are fully returned on cancel/failed; fn_verify_ledger_balance is
// clean on every path") — proves the whole vertical: hold -> vendor submission
// -> terminal state -> balance change, not just each piece in isolation.
package payout_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/payout"
	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/internal/vendorgw/mockvendor"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
)

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}

func setupPayoutTestDB(t *testing.T) *database.DBSQL {
	t.Helper()
	ctx := context.Background()

	const dbName, dbUser, dbPassword = "seev_test", "test", "secret"

	container, err := postgres.Run(ctx,
		"postgres:16.14-alpine",
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5432")
	require.NoError(t, err)

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		dbUser, dbPassword, host, port.Port(), dbName)

	require.NoError(t, testutil.ApplyServiceMigrations(migrationsSourceURL(t), dsn))

	cfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	}
	db, err := database.New(ctx, cfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

func getAccountBalance(t *testing.T, db *database.DBSQL, accountID uuid.UUID) decimal.Decimal {
	t.Helper()
	var balance int64
	err := db.QueryRowContext(context.Background(),
		`SELECT balance FROM account_balances WHERE account_id = $1`, accountID).Scan(&balance)
	require.NoError(t, err)
	return decimal.NewFromInt(balance)
}

func seedFeeRule(t *testing.T, db *database.DBSQL, userID *uuid.UUID, txType string, flat int64) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO fee_rules (id, tx_type, gateway, currency, user_id, flat_minor_units)
		VALUES ($1, $2, '', 'IDR', $3, $4)`, uuid.New(), txType, userID, flat)
	require.NoError(t, err)
}

// vendorCallCount counts payout_vendor_calls rows recorded for a request
// whose summary starts with prefix ("submit" or "query") — the audit trail
// recordVendorCall writes on every provider call, used here instead of an
// in-process counter so these assertions verify what actually landed in the
// database, the same source of truth an operator debugging a stuck payout
// would look at.
func vendorCallCount(t *testing.T, db *database.DBSQL, requestID uuid.UUID, prefix string) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM payout_vendor_calls WHERE payout_request_id = $1 AND req_summary LIKE $2`,
		requestID, prefix+"%").Scan(&count))
	return count
}

func assertLedgerBalanced(t *testing.T, db *database.DBSQL) {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity')`)
	require.NoError(t, err)
	defer rows.Close()
	assert.False(t, rows.Next(), "fn_verify_ledger_balance found an unbalanced transaction")
}

// newPayoutTestModules wires a real ledger.Module (no workers started, same
// convention as internal/payin's own integration tests) and a real
// payout.Module backed by mockvendor's PayoutProvider.
func newPayoutTestModules(db *database.DBSQL) (*testutil.LedgerHarness, *payout.Module, *mockvendor.PayoutProvider) {
	ledgerModule := testutil.NewLedgerHarness(db)

	provider := mockvendor.NewPayoutProvider(mockvendor.VendorName)
	registry := vendorgw.NewRegistry()
	registry.AddPayout(provider)

	payoutModule := payout.NewModule(db, ledgerModule, registry, nil, nil, nil, nil)
	return ledgerModule, payoutModule, provider
}

// setupFundedUser provisions a user's cash/hold/pending/frozen accounts and
// tops up their cash balance via a real money_in posting (not a raw SQL
// insert) so the balance the payout tests observe is itself
// ledger-consistent from the start.
func setupFundedUser(t *testing.T, ledgerModule *testutil.LedgerHarness, amount int64) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	userID := uuid.New()
	require.NoError(t, ledgerModule.ProvisionUser(ctx, userID, "IDR"))

	require.NoError(t, ledgerModule.Post(ctx, ledgerclient.Command{
		IdempotencyKey: "topup-" + userID.String(), Type: "money_in",
		Amount: decimal.NewFromInt(amount), UserID: userID,
		Metadata: map[string]any{"gateway": "bca"},
	}))
	return userID
}

func cashAccountID(t *testing.T, ledgerModule *testutil.LedgerHarness, userID uuid.UUID) uuid.UUID {
	t.Helper()
	accounts, err := ledgerModule.ListAccounts(context.Background(), userID)
	require.NoError(t, err)
	for _, a := range accounts {
		if a.Type == "cash" {
			return a.ID
		}
	}
	t.Fatalf("no cash account found for user %s", userID)
	return uuid.Nil
}

func mockDestination(mode string) []byte {
	dest := map[string]string{"bank_code": "014", "account_no": "1234567890"}
	if mode != "" {
		dest["mock_mode"] = mode
	}
	b, err := json.Marshal(dest)
	if err != nil {
		panic(err)
	}
	return b
}

// TestPayout_Create_InstantSettle_EndToEnd proves the synchronous path
// (docs/roadmap/archive/23 Task T3's "instant-settle" mode): Create alone drives
// created -> held -> submitted -> settled, the hold amount actually leaves
// the user's cash balance, and the ledger stays balanced throughout.
func TestPayout_Create_InstantSettle_EndToEnd(t *testing.T) {
	db := setupPayoutTestDB(t)
	ledgerModule, payoutModule, _ := newPayoutTestModules(db)
	ctx := context.Background()

	userID := setupFundedUser(t, ledgerModule, 200_000)
	cash := cashAccountID(t, ledgerModule, userID)

	id, err := payoutModule.Create(ctx, userID, decimal.NewFromInt(100_000), mockDestination(""), "test", "")
	require.NoError(t, err)

	// docs/roadmap/archive/45 Task T1: Create returns after hold+enqueue — the vendor
	// result now always comes from a separate relay dispatch pass, even in
	// "instant-settle" mode.
	req, err := payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSubmitted, req.Status, "Create must return before any vendor result — dispatch is async")

	n, err := payoutModule.DispatchPendingCommands(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	req, err = payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, mockvendor.VendorName, req.Vendor, "fallback routing rule in real Postgres must select mockvendor")
	assert.Equal(t, model.StatusSettled, req.Status)
	require.NotNil(t, req.HoldTxID)
	require.NotNil(t, req.SettleTxID)

	assert.True(t, getAccountBalance(t, db, cash).Equal(decimal.NewFromInt(100_000)),
		"cash balance must drop by exactly the settled payout amount")
	assert.Equal(t, 1, vendorCallCount(t, db, id, "submit"), "Submit must be called exactly once for a clean instant-settle flow")

	assertLedgerBalanced(t, db)
}

// TestPayout_Create_Async_ResumeJobSettles proves the async path
// (docs/roadmap/archive/23 Task T3's "async" mode + step 3's resume/polling job):
// Create leaves the request vendor_pending because the vendor hasn't
// resolved it yet; once the vendor (simulated via CompletePending)
// eventually settles it out of band, ResumeStuck's Query-based polling
// (not a fresh Submit) is what drives the request to its terminal state.
func TestPayout_Create_Async_ResumeJobSettles(t *testing.T) {
	db := setupPayoutTestDB(t)
	ledgerModule, payoutModule, provider := newPayoutTestModules(db)
	ctx := context.Background()

	userID := setupFundedUser(t, ledgerModule, 200_000)
	cash := cashAccountID(t, ledgerModule, userID)

	id, err := payoutModule.Create(ctx, userID, decimal.NewFromInt(75_000), mockDestination(mockvendor.ModeAsync), "test", "")
	require.NoError(t, err)

	n, err := payoutModule.DispatchPendingCommands(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	req, err := payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusVendorPending, req.Status, "an async submit result must leave the request pending, not terminal")
	assert.True(t, getAccountBalance(t, db, cash).Equal(decimal.NewFromInt(125_000)), "the hold amount must already be out of cash even though the vendor hasn't settled yet")

	// Simulate the vendor resolving the payout out of band, then let the
	// resume job discover it. olderThan is deliberately negative so the
	// cutoff lands in the future — every row (regardless of how recently it
	// was touched) counts as "stuck" for this immediate test run.
	provider.CompletePending(id.String(), vendorgw.PayoutSettled, "")
	resumed, failed, err := payoutModule.ResumeStuck(ctx, -time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, resumed)
	assert.Equal(t, 0, failed)

	req, err = payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSettled, req.Status)
	assert.True(t, getAccountBalance(t, db, cash).Equal(decimal.NewFromInt(125_000)), "balance must not change again on settle — the hold already left cash")
	assert.Equal(t, 1, vendorCallCount(t, db, id, "submit"), "the original Create call must be the only Submit")
	assert.Equal(t, 1, vendorCallCount(t, db, id, "query"), "the resume job must Query, never re-Submit, a vendor_pending request")

	assertLedgerBalanced(t, db)
}

// TestPayout_Create_VendorFails_MoneyReturnedToCash proves the failure
// path: a synchronous vendor rejection cancels the hold and the FULL
// amount comes back to cash, leaving the user whole.
func TestPayout_Create_VendorFails_MoneyReturnedToCash(t *testing.T) {
	db := setupPayoutTestDB(t)
	ledgerModule, payoutModule, _ := newPayoutTestModules(db)
	ctx := context.Background()

	userID := setupFundedUser(t, ledgerModule, 200_000)
	cash := cashAccountID(t, ledgerModule, userID)

	id, err := payoutModule.Create(ctx, userID, decimal.NewFromInt(50_000), mockDestination(mockvendor.ModeFail), "test", "")
	require.NoError(t, err)

	_, err = payoutModule.DispatchPendingCommands(ctx, 10)
	require.NoError(t, err)

	req, err := payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCancelled, req.Status)
	require.NotNil(t, req.SettleTxID, "SettleTxID doubles as the closing tx id for cancel too")

	assert.True(t, getAccountBalance(t, db, cash).Equal(decimal.NewFromInt(200_000)),
		"a cancelled payout must return the FULL held amount to cash")

	assertLedgerBalanced(t, db)
}

// TestPayout_Create_WithWithdrawFee_SettleChargesFee proves docs/roadmap/archive/25
// Task T2's withdraw fee: with fee rules installed, an instant-settle
// payout debits the FULL amount from hold, credits settlement amount−fee,
// and credits fee[platform] the fee — the platform actually earns revenue.
func TestPayout_Create_WithWithdrawFee_SettleChargesFee(t *testing.T) {
	db := setupPayoutTestDB(t)
	ledgerModule, payoutModule, _ := newPayoutTestModules(db)
	ctx := context.Background()

	userID := setupFundedUser(t, ledgerModule, 200_000)
	seedFeeRule(t, db, &userID, "withdraw_settle", 2_500)
	cash := cashAccountID(t, ledgerModule, userID)

	feeAccountBefore := getAccountBalance(t, db, feeAccountID(t, db, "platform"))

	id, err := payoutModule.Create(ctx, userID, decimal.NewFromInt(100_000), mockDestination(""), "test", "")
	require.NoError(t, err)

	_, err = payoutModule.DispatchPendingCommands(ctx, 10)
	require.NoError(t, err)

	req, err := payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSettled, req.Status)

	assert.True(t, getAccountBalance(t, db, cash).Equal(decimal.NewFromInt(100_000)),
		"cash must still drop by the FULL withdrawn amount regardless of fee")

	feeAccountAfter := getAccountBalance(t, db, feeAccountID(t, db, "platform"))
	assert.True(t, feeAccountAfter.Sub(feeAccountBefore).Equal(decimal.NewFromInt(2_500)),
		"fee[platform] must be credited exactly the withdraw fee")

	assertLedgerBalanced(t, db)
}

// TestPayout_Create_WithWithdrawFee_CancelledRefundsFullAmount_NoFeeCharged
// proves the business-safety property the whole "charge on settle, not
// initiate" design exists for: a withdraw that ends up CANCELLED (vendor
// rejected it) must refund the user in full — the platform must never
// pocket a fee for a withdrawal that never actually happened.
func TestPayout_Create_WithWithdrawFee_CancelledRefundsFullAmount_NoFeeCharged(t *testing.T) {
	db := setupPayoutTestDB(t)
	ledgerModule, payoutModule, _ := newPayoutTestModules(db)
	ctx := context.Background()

	userID := setupFundedUser(t, ledgerModule, 200_000)
	seedFeeRule(t, db, &userID, "withdraw_settle", 2_500)
	cash := cashAccountID(t, ledgerModule, userID)

	feeAccountBefore := getAccountBalance(t, db, feeAccountID(t, db, "platform"))

	id, err := payoutModule.Create(ctx, userID, decimal.NewFromInt(50_000), mockDestination(mockvendor.ModeFail), "test", "")
	require.NoError(t, err)

	_, err = payoutModule.DispatchPendingCommands(ctx, 10)
	require.NoError(t, err)

	req, err := payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCancelled, req.Status)

	assert.True(t, getAccountBalance(t, db, cash).Equal(decimal.NewFromInt(200_000)),
		"a cancelled withdrawal must refund the FULL amount — no fee retained")

	feeAccountAfter := getAccountBalance(t, db, feeAccountID(t, db, "platform"))
	assert.True(t, feeAccountAfter.Equal(feeAccountBefore),
		"fee[platform] must NOT be credited anything for a cancelled withdrawal")

	assertLedgerBalanced(t, db)
}

// feeAccountID looks up the seeded system fee account for a gateway
// qualifier (migrations/000002_seed_system_accounts.up.sql seeds
// fee[platform] for IDR).
func feeAccountID(t *testing.T, db *database.DBSQL, qualifier string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT id FROM accounts WHERE type = 'fee' AND system_qualifier = $1 AND currency = 'IDR'`, qualifier,
	).Scan(&id))
	return id
}

// setFeeRuleFlat updates a fee_rules row's flat_minor_units in place —
// simulates an admin re-pricing AFTER a quote already locked in the old
// fee (docs/roadmap/archive/38 Task T5's own KEY requirement).
func setFeeRuleFlat(t *testing.T, db *database.DBSQL, userID uuid.UUID, txType string, flat int64) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`UPDATE fee_rules SET flat_minor_units = $1 WHERE tx_type = $2 AND user_id = $3`, flat, txType, userID)
	require.NoError(t, err)
}

// TestPayout_QuoteHonoredAtSettle_EvenIfFeeRuleChangesBeforeSettle is
// docs/roadmap/archive/38 Task T5's KEY test: the fee actually charged at settle must
// equal the fee LOCKED IN at Create (via the quote), never a fresh
// fee_rules lookup, even when an admin re-prices in between.
func TestPayout_QuoteHonoredAtSettle_EvenIfFeeRuleChangesBeforeSettle(t *testing.T) {
	db := setupPayoutTestDB(t)
	ledgerModule, payoutModule, _ := newPayoutTestModules(db)
	ctx := context.Background()

	userID := setupFundedUser(t, ledgerModule, 200_000)
	seedFeeRule(t, db, &userID, "withdraw_settle", 2_500)
	cash := cashAccountID(t, ledgerModule, userID)
	feeAccountBefore := getAccountBalance(t, db, feeAccountID(t, db, "platform"))

	amount := decimal.NewFromInt(100_000)
	quote, err := ledgerModule.CreateQuote(ctx, userID, "withdraw_settle", "", "IDR", amount, time.Minute)
	require.NoError(t, err)
	require.True(t, quote.FeeAmount.Equal(decimal.NewFromInt(2_500)), "sanity: quote must have priced the 2500 flat fee")

	// Admin re-prices AFTER the quote was created, BEFORE the payout settles.
	setFeeRuleFlat(t, db, userID, "withdraw_settle", 9_999)

	id, err := payoutModule.Create(ctx, userID, amount, mockDestination(""), "test", quote.ID.String())
	require.NoError(t, err)

	_, err = payoutModule.DispatchPendingCommands(ctx, 10)
	require.NoError(t, err)

	req, err := payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSettled, req.Status)
	require.NotNil(t, req.FeeQuoteID, "the settled request must record which quote it consumed")
	assert.Equal(t, quote.ID, *req.FeeQuoteID)

	assert.True(t, getAccountBalance(t, db, cash).Equal(decimal.NewFromInt(100_000)),
		"cash must drop by the full withdrawn amount regardless of fee")
	feeAccountAfter := getAccountBalance(t, db, feeAccountID(t, db, "platform"))
	assert.True(t, feeAccountAfter.Sub(feeAccountBefore).Equal(decimal.NewFromInt(2_500)),
		"fee[platform] must be credited the QUOTED 2500, never the changed 9999 rule")

	assertLedgerBalanced(t, db)
}

// TestPayout_ResumeJobSettle_UsesStoredFee proves the fee quote's promise
// holds even when settle happens much later via the resume job (async
// vendor confirmation) — not just on Create's own inline synchronous path.
func TestPayout_ResumeJobSettle_UsesStoredFee(t *testing.T) {
	db := setupPayoutTestDB(t)
	ledgerModule, payoutModule, provider := newPayoutTestModules(db)
	ctx := context.Background()

	userID := setupFundedUser(t, ledgerModule, 200_000)
	seedFeeRule(t, db, &userID, "withdraw_settle", 1_200)
	feeAccountBefore := getAccountBalance(t, db, feeAccountID(t, db, "platform"))

	amount := decimal.NewFromInt(80_000)
	quote, err := ledgerModule.CreateQuote(ctx, userID, "withdraw_settle", "", "IDR", amount, time.Minute)
	require.NoError(t, err)
	require.True(t, quote.FeeAmount.Equal(decimal.NewFromInt(1_200)))

	id, err := payoutModule.Create(ctx, userID, amount, mockDestination(mockvendor.ModeAsync), "test", quote.ID.String())
	require.NoError(t, err)

	_, err = payoutModule.DispatchPendingCommands(ctx, 10)
	require.NoError(t, err)

	req, err := payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusVendorPending, req.Status)

	// Simulate time passing and an admin re-pricing BEFORE the resume job
	// ever settles this request.
	setFeeRuleFlat(t, db, userID, "withdraw_settle", 7_777)

	provider.CompletePending(id.String(), vendorgw.PayoutSettled, "")
	resumed, failed, err := payoutModule.ResumeStuck(ctx, -time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, resumed)
	assert.Equal(t, 0, failed)

	req, err = payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSettled, req.Status)

	feeAccountAfter := getAccountBalance(t, db, feeAccountID(t, db, "platform"))
	assert.True(t, feeAccountAfter.Sub(feeAccountBefore).Equal(decimal.NewFromInt(1_200)),
		"resume-job settle must use the STORED quoted fee (1200), never re-resolve fee_rules (7777)")

	assertLedgerBalanced(t, db)
}

// TestPayout_QuoteExpired_Returns422_NoHold_LedgerUntouched_RowRejected
// proves docs/roadmap/archive/38 Task T5's anti-burn ordering: a quote that's already
// expired by the time Create runs must reject BEFORE any hold is posted —
// no money moves, the ledger is untouched, and the row lands in the
// terminal 'rejected' status (never 'created' forever, never 'held').
func TestPayout_QuoteExpired_Returns422_NoHold_LedgerUntouched_RowRejected(t *testing.T) {
	db := setupPayoutTestDB(t)
	ledgerModule, payoutModule, _ := newPayoutTestModules(db)
	ctx := context.Background()

	userID := setupFundedUser(t, ledgerModule, 200_000)
	cash := cashAccountID(t, ledgerModule, userID)
	cashBefore := getAccountBalance(t, db, cash)

	amount := decimal.NewFromInt(50_000)
	quote, err := ledgerModule.CreateQuote(ctx, userID, "withdraw_settle", "", "IDR", amount, time.Minute)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE fee_quotes SET expires_at = now() - interval '1 minute' WHERE id = $1`, quote.ID)
	require.NoError(t, err)

	id, err := payoutModule.Create(ctx, userID, amount, mockDestination(""), "test", quote.ID.String())
	require.Error(t, err)
	require.NotEqual(t, uuid.Nil, id, "Create still returns the row id even on rejection, matching every other Create error path")

	req, err := payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusRejected, req.Status)
	assert.Nil(t, req.HoldTxID, "no hold was ever posted for a rejected quote")

	assert.True(t, getAccountBalance(t, db, cash).Equal(cashBefore), "cash must be completely untouched — the ledger was never called")

	assertLedgerBalanced(t, db)
}

// TestPayout_ResumeStuck_SubmittedWithNoCommand_RecoversAndSettles proves
// docs/roadmap/archive/45 Task T1/K1's genuine crash-gap recovery: a 'submitted'
// request whose command row is missing entirely (EnqueueInitialSubmit's own
// atomicity already rules this out in normal operation — this simulates a
// manual/corrupted-data recovery scenario, the belt-and-braces case the
// resume job's HasAnyCommand check exists for) gets a fresh command
// inserted by the resume job, which the relay's own dispatch pass then
// settles — proving the resume-job-inserts / relay-dispatches division of
// labor (K2) end to end.
func TestPayout_ResumeStuck_SubmittedWithNoCommand_RecoversAndSettles(t *testing.T) {
	db := setupPayoutTestDB(t)
	ledgerModule, payoutModule, _ := newPayoutTestModules(db)
	ctx := context.Background()

	userID := setupFundedUser(t, ledgerModule, 200_000)
	cash := cashAccountID(t, ledgerModule, userID)

	id, err := payoutModule.Create(ctx, userID, decimal.NewFromInt(60_000), mockDestination(""), "test", "")
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `DELETE FROM payout_vendor_commands WHERE payout_request_id = $1`, id)
	require.NoError(t, err)

	resumed, failed, err := payoutModule.ResumeStuck(ctx, -time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, resumed)
	assert.Equal(t, 0, failed)

	n, err := payoutModule.DispatchPendingCommands(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "resume must have inserted exactly one fresh command for the relay to dispatch")

	req, err := payoutModule.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSettled, req.Status)
	assert.True(t, getAccountBalance(t, db, cash).Equal(decimal.NewFromInt(140_000)))
	assert.Equal(t, 1, vendorCallCount(t, db, id, "submit"))

	assertLedgerBalanced(t, db)
}
