//go:build integration

// White-box (package payout) so this can drive m.submit() directly against
// a real Postgres-backed repository and ledger, mirroring
// race_integration_test.go's pattern — reuses its setupRaceTestDB,
// raceGetBalance, and raceAssertLedgerBalanced helpers directly since this
// file shares the same package and build tag.
package payout

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/payout/repository"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
)

// TestFailover_ConcurrentSubmit_RaceResumeJobVsFailover is docs/plan/40 Task
// T3's required race scenario (d): the resume job retrying a stuck request
// concurrently with another in-flight submit() attempt for the SAME
// request (e.g. Create's own initial submit still unwinding) must never
// produce a double payout, even when one caller fails over to a different
// vendor while another is mid-flight. Every concurrent submit() call reads
// the same deterministic routing candidates and gets an idempotent-per-
// caller vendor result, so all callers converge on the SAME winning vendor
// and the SAME settle idempotency key — the ledger's own dedup (not this
// package's) is what makes the loser's Post a safe no-op, exactly like
// TestDoubleCallback_ConcurrentSettle_ExactlyOnePosted already proves for a
// single-vendor double-callback.
func TestFailover_ConcurrentSubmit_RaceResumeJobVsFailover(t *testing.T) {
	db := setupRaceTestDB(t)
	ledgerModule := testutil.NewLedgerHarness(db)
	ctx := context.Background()

	userID := uuid.New()
	require.NoError(t, ledgerModule.ProvisionUser(ctx, userID, "IDR"))
	require.NoError(t, ledgerModule.Post(ctx, ledgerclient.Command{
		IdempotencyKey: "topup-" + userID.String(), Type: "money_in",
		Amount: decimal.NewFromInt(200_000), UserID: userID,
		Metadata: map[string]any{"gateway": "bca"},
	}))

	dest, _ := json.Marshal(map[string]string{"bank_code": "014", "account_no": "1234567890"})
	req := model.PayoutRequest{
		ID: generalutil.NewV7(), UserID: userID, Amount: decimal.NewFromInt(100_000), Currency: "IDR",
		Vendor: "vendorA", Destination: dest, CreatedBy: "test",
	}
	repo := repository.NewRepository(db)
	require.NoError(t, repo.Insert(ctx, req))

	// providerA always returns a definitive business rejection (never an
	// error) — deterministic and vendor-agnostic of destination content, so
	// every concurrent caller gets the SAME classification ("rejected") and
	// therefore the SAME failover decision. providerB always settles.
	providerA := &stubPayoutProvider{name: "vendorA", submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
		return vendorgw.PayoutResult{Status: vendorgw.PayoutFailed, Reason: "declined by vendorA"}, nil
	}}
	providerB := &stubPayoutProvider{name: "vendorB", submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
		return vendorgw.PayoutResult{Status: vendorgw.PayoutSettled}, nil
	}}
	registry := vendorgw.NewRegistry()
	registry.AddPayout(providerA)
	registry.AddPayout(providerB)

	routing := multiVendorRouting{candidates: []model.RoutingCandidate{
		{Vendor: "vendorA", Gateway: "bca"},
		{Vendor: "vendorB", Gateway: "bca"},
	}}

	m := &Module{repo: repo, poster: ledgerModule, registry: registry, routing: routing, logger: discardLogger()}
	require.NoError(t, m.hold(ctx, req))

	const concurrency = 10
	var wg sync.WaitGroup
	errs := make([]error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = m.submit(ctx, req.ID)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "concurrent submit() call %d must not surface an error (lost races reconcile silently)", i)
	}

	final, err := repo.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSettled, final.Status)
	assert.Equal(t, "vendorB", final.Vendor, "every concurrent caller must converge on the SAME winning vendor")

	var txCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM ledger_transactions WHERE idempotency_key = $1`, settleIdempotencyKey(req.ID),
	).Scan(&txCount))
	assert.Equal(t, 1, txCount, "exactly one withdraw_settle transaction despite %d concurrent submit() callers", concurrency)

	accounts, err := ledgerModule.ListAccounts(ctx, userID)
	require.NoError(t, err)
	var cash uuid.UUID
	for _, a := range accounts {
		if a.Type == "cash" {
			cash = a.ID
		}
	}
	require.NotEqual(t, uuid.Nil, cash)
	assert.True(t, raceGetBalance(t, db, cash).Equal(decimal.NewFromInt(100_000)),
		"cash must reflect exactly ONE settle (200_000 funded - 100_000 payout) despite %d concurrent callers", concurrency)

	raceAssertLedgerBalanced(t, db)

	// No call may ever have landed 'uncertain' — every provider.Submit in
	// this test returns a definitive result (nil error), so the anti-
	// double-payout pin (mayFailover == false) can never have been
	// triggered by an infra failure here; only by the eventual 'accepted'
	// once vendorB settles.
	calls, err := repo.ListVendorCalls(ctx, req.ID)
	require.NoError(t, err)
	for _, c := range calls {
		assert.NotEqual(t, model.VendorCallUncertain, c.Outcome, "no call should be uncertain in this all-synchronous scenario")
	}
}
