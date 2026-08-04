//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T3's (K7) idempotency-key digest
// tombstone end to end against a real Postgres: dedup before/after raw
// redaction, distinct scopes stay distinct, a conflicting retry is
// rejected (not silently treated as a duplicate), concurrent retries have
// exactly one monetary effect, bounded backfill, and the rotation-transition
// raw-key fallback. Reuses setupSchemaTestDB/newService/createUserCashAccount/
// getBalance/schemaTestDigestRing (schema_contract_test.go, same package).
package ledger_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/processors"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
)

// TestIdempotency_SameKeyScope_DeduplicatesBeforeAndAfterRawRedaction is
// T3's own required test: "same key/scope deduplicates before and after
// raw redaction."
func TestIdempotency_SameKeyScope_DeduplicatesBeforeAndAfterRawRedaction(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userID := uuid.New()
	cash := createUserCashAccount(t, db, userID)

	cmd := processors.Command{
		IdempotencyKey: "topup-dedup-1", Type: "money_in",
		Amount: decimal.NewFromInt(50_000), UserID: userID,
		Metadata: map[string]any{"gateway": "bca"},
	}
	require.NoError(t, svc.Handle(ctx, cmd))
	require.True(t, decimal.NewFromInt(50_000).Equal(getBalance(t, db, cash)))

	// Retry BEFORE any redaction — must dedupe via the (still-load-bearing)
	// raw unique index / digest, same result either way.
	require.NoError(t, svc.Handle(ctx, cmd), "a same-key retry before redaction must be idempotent success")
	require.True(t, decimal.NewFromInt(50_000).Equal(getBalance(t, db, cash)))

	// Simulate the T3 retention job having already redacted this row's raw
	// key/scope (fn_retention_purge_transactions_idempotency_raw) — the
	// digest, written at Insert time, must still be there to catch a
	// retry.
	_, err := db.ExecContext(ctx, `
		UPDATE ledger_transactions SET idempotency_key = NULL, idempotency_scope = NULL
		WHERE idempotency_key_digest IS NOT NULL AND id IN (
			SELECT id FROM ledger_transactions WHERE amount = 50000 AND type = 'money_in'
		)`)
	require.NoError(t, err)

	require.NoError(t, svc.Handle(ctx, cmd), "a same-key retry AFTER raw redaction must still be idempotent success")
	require.True(t, decimal.NewFromInt(50_000).Equal(getBalance(t, db, cash)), "redaction must never allow a duplicate monetary effect")
}

// TestIdempotency_SameKeyDifferentScope_RemainsDistinct is T3's own
// required test: "same key with a different scope remains distinct."
func TestIdempotency_SameKeyDifferentScope_RemainsDistinct(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userID := uuid.New()
	cash := createUserCashAccount(t, db, userID)

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "shared-key", IdempotencyScope: "scope-a", Type: "money_in",
		Amount: decimal.NewFromInt(10_000), UserID: userID, Metadata: map[string]any{"gateway": "bca"},
	}))
	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "shared-key", IdempotencyScope: "scope-b", Type: "money_in",
		Amount: decimal.NewFromInt(10_000), UserID: userID, Metadata: map[string]any{"gateway": "bca"},
	}))

	require.True(t, decimal.NewFromInt(20_000).Equal(getBalance(t, db, cash)), "different scopes must post as two distinct transactions")

	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_transactions WHERE idempotency_key = 'shared-key'`).Scan(&count))
	require.Equal(t, 2, count)
}

// TestIdempotency_ConflictingAmount_ReturnsConflict is T3's own required
// test: "conflicting amount/type returns the original idempotency
// conflict."
func TestIdempotency_ConflictingAmount_ReturnsConflict(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userID := uuid.New()
	cash := createUserCashAccount(t, db, userID)

	const key = "topup-conflict-1"
	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: key, Type: "money_in",
		Amount: decimal.NewFromInt(10_000), UserID: userID, Metadata: map[string]any{"gateway": "bca"},
	}))

	err := svc.Handle(ctx, processors.Command{
		IdempotencyKey: key, Type: "money_in",
		Amount: decimal.NewFromInt(99_999), UserID: userID, Metadata: map[string]any{"gateway": "bca"},
	})
	require.ErrorIs(t, err, apperror.ErrIdempotencyConflict, "the same key reused with a different amount must never be treated as a legitimate retry")
	require.True(t, decimal.NewFromInt(10_000).Equal(getBalance(t, db, cash)), "a rejected conflicting retry must have no monetary effect")
}

// TestIdempotency_ConcurrentRetries_ExactlyOneMonetaryEffect is T3's own
// required test: "concurrent retries have exactly one monetary effect."
func TestIdempotency_ConcurrentRetries_ExactlyOneMonetaryEffect(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userID := uuid.New()
	cash := createUserCashAccount(t, db, userID)

	const attempts = 20
	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = svc.Handle(ctx, processors.Command{
				IdempotencyKey: "topup-concurrent-1", Type: "money_in",
				Amount: decimal.NewFromInt(75_000), UserID: userID, Metadata: map[string]any{"gateway": "bca"},
			})
		}(i)
	}
	wg.Wait()

	for _, err := range results {
		require.NoError(t, err, "every concurrent retry of an identical request must resolve to idempotent success, never an error")
	}
	require.True(t, decimal.NewFromInt(75_000).Equal(getBalance(t, db, cash)), "N concurrent identical retries must have exactly one monetary effect")

	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_transactions WHERE idempotency_key = 'topup-concurrent-1'`).Scan(&count))
	require.Equal(t, 1, count)
}

// TestIdempotency_ConcurrentRetries_AfterRawRedaction_ExactlyOneMonetaryEffect
// is the acceptance checklist's own combined claim ("concurrent replay
// after redaction has exactly one monetary effect") — distinct from the
// two tests above: neither the sequential before/after-redaction test nor
// the concurrent-before-redaction test alone proves this specific
// combination, since a race only stresses the DB unique constraint the
// SAME way regardless of whether raw key/scope happens to still be
// present — this test earns that claim directly rather than assuming it.
func TestIdempotency_ConcurrentRetries_AfterRawRedaction_ExactlyOneMonetaryEffect(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userID := uuid.New()
	cash := createUserCashAccount(t, db, userID)

	const key = "topup-concurrent-redacted-1"
	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: key, Type: "money_in",
		Amount: decimal.NewFromInt(30_000), UserID: userID, Metadata: map[string]any{"gateway": "bca"},
	}))
	require.True(t, decimal.NewFromInt(30_000).Equal(getBalance(t, db, cash)))

	_, err := db.ExecContext(ctx, `
		UPDATE ledger_transactions SET idempotency_key = NULL, idempotency_scope = NULL
		WHERE idempotency_key_digest IS NOT NULL AND amount = 30000 AND type = 'money_in'`)
	require.NoError(t, err)

	const attempts = 20
	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = svc.Handle(ctx, processors.Command{
				IdempotencyKey: key, Type: "money_in",
				Amount: decimal.NewFromInt(30_000), UserID: userID, Metadata: map[string]any{"gateway": "bca"},
			})
		}(i)
	}
	wg.Wait()

	for _, err := range results {
		require.NoError(t, err, "every concurrent retry against an already-redacted row must still resolve to idempotent success")
	}
	require.True(t, decimal.NewFromInt(30_000).Equal(getBalance(t, db, cash)), "concurrent replay AFTER redaction must have exactly one monetary effect")

	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_transactions WHERE amount = 30000 AND type = 'money_in'`).Scan(&count))
	require.Equal(t, 1, count)
}

// TestTransactionRepository_BackfillOnce_RestartableEqualTimestamps is
// T3's own required test ("backfill every existing transaction and prove
// there are no collisions or missing versions"), same restartable/
// equal-timestamp/plaintext-absence-scan shape as every T2.5 repository's
// own BackfillOnce test.
func TestTransactionRepository_BackfillOnce_RestartableEqualTimestamps(t *testing.T) {
	db := setupSchemaTestDB(t)
	ctx := context.Background()

	userID := uuid.New()
	cash := createUserCashAccount(t, db, userID)

	const rowCount = 20
	sharedCreatedAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]uuid.UUID, rowCount)
	for i := 0; i < rowCount; i++ {
		ids[i] = uuid.New()
		_, err := db.ExecContext(ctx, `
			INSERT INTO ledger_transactions
				(id, idempotency_key, idempotency_scope, type, status, amount, currency,
				 destination_account_id, created_at, updated_at)
			VALUES ($1, $2, NULL, 'money_in', 'posted', 1000, 'IDR', $3, $4, $4)`,
			ids[i], fmt.Sprintf("legacy-key-%d", i), cash, sharedCreatedAt)
		require.NoError(t, err)
	}

	repo := repository.NewTransactionRepository(db, schemaTestDigestRing())
	total := 0
	for i := 0; i < rowCount+5; i++ {
		n, err := repo.BackfillOnce(ctx, 3)
		require.NoError(t, err)
		total += n
		if n == 0 {
			break
		}
	}
	require.Equal(t, rowCount, total)

	var remaining int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT count(*) FROM ledger_transactions
		WHERE idempotency_key IS NOT NULL AND idempotency_key_digest IS NULL`).Scan(&remaining))
	require.Zero(t, remaining, "no row with a raw idempotency key may still be missing a digest after backfill completes")

	seenDigests := make(map[string]bool, rowCount)
	for _, id := range ids {
		var digest []byte
		require.NoError(t, db.QueryRowContext(ctx, `SELECT idempotency_key_digest FROM ledger_transactions WHERE id = $1`, id).Scan(&digest))
		require.NotEmpty(t, digest)
		require.False(t, seenDigests[string(digest)], "distinct idempotency keys must never collide onto the same digest")
		seenDigests[string(digest)] = true
	}

	n, err := repo.BackfillOnce(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

// TestFindConflictOrDuplicate_RotationFallbackViaRawKey is T3's own
// required test: "current/previous key versions work during rotation."
// Simulates the exact transitional window K7 describes: a row's digest is
// still under the OLD key version (not yet backfilled to the new
// current), so a fresh digest computed under the NEW current version
// legitimately doesn't match — the raw-key fallback is what still
// catches the duplicate.
func TestFindConflictOrDuplicate_RotationFallbackViaRawKey(t *testing.T) {
	db := setupSchemaTestDB(t)
	ctx := context.Background()

	oldKey := make([]byte, 32)
	for i := range oldKey {
		oldKey[i] = byte(i + 61)
	}
	newKey := make([]byte, 32)
	for i := range newKey {
		newKey[i] = byte(i + 67)
	}
	oldRing, err := cryptox.NewDigestRing(map[int][]byte{1: oldKey}, 1)
	require.NoError(t, err)
	rotatedRing, err := cryptox.NewDigestRing(map[int][]byte{1: oldKey, 2: newKey}, 2)
	require.NoError(t, err)

	userID := uuid.New()
	cash := createUserCashAccount(t, db, userID)

	// Insert directly through the OLD-key-only repository — its digest is
	// version 1. Written 'posted' directly (bypassing the service layer,
	// which always starts a row 'pending') since this test only needs a
	// terminal row to look up, not a real posting.
	oldRepo := repository.NewTransactionRepository(db, oldRing)
	txID := uuid.New()
	require.NoError(t, db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if err := oldRepo.Insert(ctx, tx, repository.InsertTransactionParams{
			ID: txID, IdempotencyKey: "rotation-fallback-key", Type: "money_in",
			Amount: decimal.NewFromInt(5_000), Currency: "IDR",
			DestinationAccountID: &cash,
		}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE ledger_transactions SET status = 'posted' WHERE id = $1`, txID)
		return err
	}))

	// Now look up the SAME (key, scope) through a repository whose ring
	// has ALREADY rotated to version 2 (current), simulating the window
	// before rotation backfill has caught this row up.
	rotatedRepo := repository.NewTransactionRepository(db, rotatedRing)
	require.NoError(t, db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		status, conflict, findErr := rotatedRepo.FindConflictOrDuplicate(ctx, tx, "rotation-fallback-key", nil, "money_in", decimal.NewFromInt(5_000), "IDR")
		require.NoError(t, findErr)
		require.False(t, conflict)
		require.Equal(t, "posted", status, "the raw-key fallback must still find the row even though its stored digest is under the OLD key version")
		return nil
	}))
}

// TestNewTransactionRepository_NilRing_Panics is T3's own required test:
// "missing/unknown key versions fail closed" at the construction
// boundary — a repository must never silently run without a digest ring.
func TestNewTransactionRepository_NilRing_Panics(t *testing.T) {
	db := setupSchemaTestDB(t)
	require.Panics(t, func() {
		repository.NewTransactionRepository(db, nil)
	})
}
