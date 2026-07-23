//go:build integration

// White-box (package payout) so this can drive m.enqueueSubmit/dispatchOne
// directly against a real Postgres-backed repository and ledger, mirroring
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

// TestFailover_ConcurrentDispatch_RaceRelayReplicasVsFailover is
// docs/roadmap/archive/40 Task T3's required race scenario (d), re-verified under
// docs/roadmap/archive/45 Task T1's outbox architecture: N concurrent relay-replica
// dispatch passes racing the SAME live command must never produce a
// double payout. Unlike the pre-outbox synchronous submit() (which had to
// defend against N truly concurrent in-process callers), the partial
// unique index (idx_payout_vendor_commands_one_live, docs/roadmap/archive/45 T0)
// structurally guarantees at most ONE command is ever live per request —
// FOR UPDATE SKIP LOCKED then guarantees at most one of N concurrent
// DispatchPendingCommands callers ever claims and dispatches it. This test
// proves that guarantee holds through a full reject-then-failover-then-
// settle pipeline, with N concurrent callers racing at EVERY stage, not
// just a single dispatch.
func TestFailover_ConcurrentDispatch_RaceRelayReplicasVsFailover(t *testing.T) {
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

	commandRepo := repository.NewVendorCommandRepository(db)
	m := &Module{repo: repo, commandRepo: commandRepo, poster: ledgerModule, registry: registry, routing: routing, logger: discardLogger()}
	require.NoError(t, m.hold(ctx, req))
	require.NoError(t, m.enqueueSubmit(ctx, req.ID, "vendorA"))

	const concurrency = 10
	// Round 1: vendorA's live command exists exactly once — N concurrent
	// dispatch passes race to claim it; vendorA rejects, so this round
	// ends with a failover to vendorB enqueued as attempt 2.
	dispatchConcurrently(ctx, m, concurrency)
	// Round 2: vendorB's live command (attempt 2) — same race, vendorB
	// settles this time.
	dispatchConcurrently(ctx, m, concurrency)

	final, err := repo.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSettled, final.Status)
	assert.Equal(t, "vendorB", final.Vendor, "every concurrent caller must converge on the SAME winning vendor")

	var txCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM ledger_transactions WHERE idempotency_key = $1`, settleIdempotencyKey(req.ID),
	).Scan(&txCount))
	assert.Equal(t, 1, txCount, "exactly one withdraw_settle transaction despite %d concurrent dispatch callers per round", concurrency)

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

// dispatchConcurrently fires n concurrent DispatchPendingCommands(ctx, 1)
// calls at m — with at most one live command ever present for a given
// request (idx_payout_vendor_commands_one_live, docs/roadmap/archive/45 T0), FOR
// UPDATE SKIP LOCKED guarantees at most one of these n callers actually
// claims and dispatches it; the rest simply claim nothing.
func dispatchConcurrently(ctx context.Context, m *Module, n int) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.DispatchPendingCommands(ctx, 1)
		}()
	}
	wg.Wait()
}
