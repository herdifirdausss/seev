//go:build integration

// Proves docs/plan/38 Task T4: fee quote consumption inside execTransfer,
// against a real Postgres (same throwaway-container pattern as
// schema_contract_test.go, whose helpers — setupSchemaTestDB, newService,
// createUserCashAccount — this file reuses directly, same package).
package ledger_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/feepolicy"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/pkg/database"
)

// systemFeeAccountPlatformIDR is the fee[platform][IDR] system account
// seeded by migrations/ledger/000002_seed_system_accounts (fixed, well-known
// id) — every setupSchemaTestDB call gets a fresh throwaway container, so
// this account always starts at balance 0 in each test below.
const systemFeeAccountPlatformIDR = "00000000-0000-0000-0000-000000000003"

func readAccountBalance(t *testing.T, db *database.DBSQL, accountID string) int64 {
	t.Helper()
	var balance int64
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT balance FROM account_balances WHERE account_id = $1`, accountID).Scan(&balance))
	return balance
}

func countLedgerTransactionsByKey(t *testing.T, db *database.DBSQL, idempotencyKey string) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM ledger_transactions WHERE idempotency_key = $1`, idempotencyKey).Scan(&count))
	return count
}

// TestSchemaContract_ExecTransfer_QuoteHonoredExactly_EvenIfFeeRuleChangesAfterQuote
// is the KEY test for T4: the fee actually charged must equal the fee shown
// at quote time, never a fresh fee_rules lookup at posting time, even when
// an admin changes the rule in between.
func TestSchemaContract_ExecTransfer_QuoteHonoredExactly_EvenIfFeeRuleChangesAfterQuote(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	policy := feepolicy.New(db)
	ctx := context.Background()

	userA, userB := uuid.New(), uuid.New()
	createUserCashAccount(t, db, userA)
	createUserCashAccount(t, db, userB)
	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "quote-fee-topup", Type: "money_in", Amount: decimal.NewFromInt(1_000_000),
		UserID: userA, Metadata: map[string]any{"gateway": "bca"},
	}))

	rule, err := policy.Create(ctx, feepolicy.Rule{
		TxType: "transfer_p2p", Currency: "IDR", FlatMinorUnits: 500, FeeGateway: "platform", Enabled: true,
	})
	require.NoError(t, err)

	q, err := policy.CreateQuote(ctx, userA, "transfer_p2p", "", "IDR", decimal.NewFromInt(100_000), time.Minute)
	require.NoError(t, err)
	require.True(t, q.FeeAmount.Equal(decimal.NewFromInt(500)), "sanity: quote must have priced the 500 flat fee")

	// Admin changes the rule AFTER the quote was created but BEFORE posting.
	rule.FlatMinorUnits = 9999
	_, err = policy.Update(ctx, rule)
	require.NoError(t, err)

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "quote-fee-transfer", Type: "transfer_p2p", Amount: decimal.NewFromInt(100_000),
		UserID: userA, TargetUserID: userB, QuoteID: q.ID.String(),
	}))

	feeBalance := readAccountBalance(t, db, systemFeeAccountPlatformIDR)
	assert.Equal(t, int64(500), feeBalance, "fee charged must be the QUOTED 500, never the changed 9999 rule")
}

func TestSchemaContract_ExecTransfer_QuoteExpired_RollsBackEntirely_NoTxNoEntries(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	policy := feepolicy.New(db)
	ctx := context.Background()

	userA, userB := uuid.New(), uuid.New()
	createUserCashAccount(t, db, userA)
	createUserCashAccount(t, db, userB)
	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "expired-topup", Type: "money_in", Amount: decimal.NewFromInt(1_000_000),
		UserID: userA, Metadata: map[string]any{"gateway": "bca"},
	}))

	q, err := policy.CreateQuote(ctx, userA, "transfer_p2p", "", "IDR", decimal.NewFromInt(50_000), time.Minute)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE fee_quotes SET expires_at = now() - interval '1 minute' WHERE id = $1`, q.ID)
	require.NoError(t, err)

	err = svc.Handle(ctx, processors.Command{
		IdempotencyKey: "expired-transfer", Type: "transfer_p2p", Amount: decimal.NewFromInt(50_000),
		UserID: userA, TargetUserID: userB, QuoteID: q.ID.String(),
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrQuoteExpired)
	assert.Equal(t, 0, countLedgerTransactionsByKey(t, db, "expired-transfer"),
		"a rejected quote consumption must roll back the header insert too — no tx row at all")
}

func TestSchemaContract_ExecTransfer_QuoteMismatch_RollsBackAndQuoteStaysUsable(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	policy := feepolicy.New(db)
	ctx := context.Background()

	userA, userB := uuid.New(), uuid.New()
	createUserCashAccount(t, db, userA)
	createUserCashAccount(t, db, userB)
	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "mismatch-topup", Type: "money_in", Amount: decimal.NewFromInt(1_000_000),
		UserID: userA, Metadata: map[string]any{"gateway": "bca"},
	}))

	q, err := policy.CreateQuote(ctx, userA, "transfer_p2p", "", "IDR", decimal.NewFromInt(50_000), time.Minute)
	require.NoError(t, err)

	// Amount attempted differs from what was quoted.
	err = svc.Handle(ctx, processors.Command{
		IdempotencyKey: "mismatch-transfer", Type: "transfer_p2p", Amount: decimal.NewFromInt(60_000),
		UserID: userA, TargetUserID: userB, QuoteID: q.ID.String(),
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrQuoteMismatch)
	assert.Equal(t, 0, countLedgerTransactionsByKey(t, db, "mismatch-transfer"))

	var consumedAt sql.NullTime
	require.NoError(t, db.QueryRowContext(ctx, `SELECT consumed_at FROM fee_quotes WHERE id = $1`, q.ID).Scan(&consumedAt))
	assert.False(t, consumedAt.Valid, "a mismatched attempt must NOT burn the quote")

	// Retrying with the CORRECT amount must still succeed — the quote survived.
	err = svc.Handle(ctx, processors.Command{
		IdempotencyKey: "mismatch-transfer-retry", Type: "transfer_p2p", Amount: decimal.NewFromInt(50_000),
		UserID: userA, TargetUserID: userB, QuoteID: q.ID.String(),
	})
	require.NoError(t, err)
}

// TestSchemaContract_ExecTransfer_ReplayAfterQuoteSuccess_IdempotentNoReconsumption
// proves docs/plan/38 Task T4 step 5: the idempotency-key lookup runs BEFORE
// quote consumption, so a replay of an already-posted request returns the
// original success WITHOUT ever attempting to re-consume the (already
// consumed) quote.
func TestSchemaContract_ExecTransfer_ReplayAfterQuoteSuccess_IdempotentNoReconsumption(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	policy := feepolicy.New(db)
	ctx := context.Background()

	userA, userB := uuid.New(), uuid.New()
	createUserCashAccount(t, db, userA)
	createUserCashAccount(t, db, userB)
	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "replay-topup", Type: "money_in", Amount: decimal.NewFromInt(1_000_000),
		UserID: userA, Metadata: map[string]any{"gateway": "bca"},
	}))

	q, err := policy.CreateQuote(ctx, userA, "transfer_p2p", "", "IDR", decimal.NewFromInt(50_000), time.Minute)
	require.NoError(t, err)

	cmd := processors.Command{
		IdempotencyKey: "replay-transfer", Type: "transfer_p2p", Amount: decimal.NewFromInt(50_000),
		UserID: userA, TargetUserID: userB, QuoteID: q.ID.String(),
	}
	require.NoError(t, svc.Handle(ctx, cmd))

	// Replay: identical command, same idempotency key. The quote is already
	// consumed at this point — a naive re-consumption attempt would return
	// ErrQuoteExpired, but the idempotency gate must short-circuit first.
	err = svc.Handle(ctx, cmd)
	require.NoError(t, err, "replay of an already-posted request must succeed idempotently despite the quote already being consumed")
	assert.Equal(t, 1, countLedgerTransactionsByKey(t, db, "replay-transfer"), "replay must not create a second row")
}

// TestSchemaContract_ExecTransfer_ConcurrentDifferentTransfersSameQuote_ExactlyOneSucceeds
// proves the concurrency requirement: N different transfers (different
// idempotency keys) racing to consume the SAME single-use quote — exactly
// one must succeed, every other must fail with ErrQuoteExpired.
func TestSchemaContract_ExecTransfer_ConcurrentDifferentTransfersSameQuote_ExactlyOneSucceeds(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	policy := feepolicy.New(db)
	ctx := context.Background()

	userA, userB := uuid.New(), uuid.New()
	createUserCashAccount(t, db, userA)
	createUserCashAccount(t, db, userB)
	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "race-topup", Type: "money_in", Amount: decimal.NewFromInt(1_000_000),
		UserID: userA, Metadata: map[string]any{"gateway": "bca"},
	}))

	q, err := policy.CreateQuote(ctx, userA, "transfer_p2p", "", "IDR", decimal.NewFromInt(10_000), time.Minute)
	require.NoError(t, err)

	const n = 5
	var wins, losses int64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			herr := svc.Handle(ctx, processors.Command{
				IdempotencyKey: fmt.Sprintf("race-transfer-%d", i), Type: "transfer_p2p", Amount: decimal.NewFromInt(10_000),
				UserID: userA, TargetUserID: userB, QuoteID: q.ID.String(),
			})
			if herr == nil {
				atomic.AddInt64(&wins, 1)
			} else {
				assert.ErrorIs(t, herr, apperror.ErrQuoteExpired)
				atomic.AddInt64(&losses, 1)
			}
		}(i)
	}
	wg.Wait()
	assert.Equal(t, int64(1), wins, "exactly one concurrent transfer must win the single-use quote")
	assert.Equal(t, int64(n-1), losses)
}

// TestSchemaContract_ExecTransfer_NoQuoteID_BehavesExactlyAsBefore proves
// docs/plan/38 Task T4 step 4: an ordinary transfer without quote_id (and
// without a manually-supplied fee_amount/fee_gateway — those are normally
// injected by the transport layer's buildMetadata, out of scope for this
// direct svc.Handle call) posts exactly as it did before this feature
// existed — the new "── 1b. FEE QUOTE CONSUMPTION" block is a complete
// no-op when cmd.QuoteID is empty.
func TestSchemaContract_ExecTransfer_NoQuoteID_BehavesExactlyAsBefore(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userA, userB := uuid.New(), uuid.New()
	createUserCashAccount(t, db, userA)
	createUserCashAccount(t, db, userB)
	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "noquote-topup", Type: "money_in", Amount: decimal.NewFromInt(1_000_000),
		UserID: userA, Metadata: map[string]any{"gateway": "bca"},
	}))

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "noquote-transfer", Type: "transfer_p2p", Amount: decimal.NewFromInt(100_000),
		UserID: userA, TargetUserID: userB,
	}))

	assert.Equal(t, int64(0), readAccountBalance(t, db, systemFeeAccountPlatformIDR), "no fee_rule configured and no quote used — fee account must be untouched")
}
