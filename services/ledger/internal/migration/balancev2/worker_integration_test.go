//go:build integration

package balancev2

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	migrationkit "github.com/herdifirdausss/seev/internal/platform/migration"
)

// TestBackfillOnce_VersionSafeUpsert is the critical invariant test:
// a stale backfill row (older source version) must never overwrite a newer
// live-write row that was inserted by the dual-write path.
//
// Setup:
//  1. Seed a cash account with balance 1000 at version 95 (the "stale" backfill view).
//  2. Simulate a dual-write: insert an account_balances_v2 row at version 101
//     (representing a posting that happened while backfill was in progress).
//  3. Run BackfillOnce with a source row capped at version 95.
//
// Expected: the v2 row still has source_version=101. The backfill must not
// regress the target to version 95.
func TestBackfillOnce_VersionSafeUpsert(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	ctx := context.Background()
	cfg := newTestConfig()
	cfg.Enabled = true
	cfg.BackfillBatchSize = 100
	runtime := NewRuntime(db, cfg, slog.Default())

	// Initialize migration and advance to Backfilling.
	require.NoError(t, runtime.Initialize(ctx, "test"))
	repo := runtime.Controls()
	m, err := repo.GetByName(ctx, MigrationName)
	require.NoError(t, err)
	m = advanceTo(t, repo, m, migrationkit.Validated)
	m = advanceTo(t, repo, m, migrationkit.TargetReady)
	m = advanceTo(t, repo, m, migrationkit.Backfilling)
	_ = m

	// Seed account at current v1 balance = 1000, which will trigger a few
	// UPDATE calls and advance the version counter.
	accountID := seedAccount(t, db, "cash", "IDR")
	// Force the v1 balance to a low version: set balance once (version becomes 1).
	adjustBalance(t, db, accountID, 50_000)

	// Simulate a live write that pushed v2 to a higher version (101).
	// We insert directly into account_balances_v2 as if the dual-write path
	// already ran for this account at a newer transaction.
	newerVersion := int64(101)
	_, err = db.ExecContext(ctx, `
		INSERT INTO account_balances_v2
			(account_id, account_type, currency, allow_negative,
			 available_amount, reserved_amount, pending_amount, restricted_amount,
			 source_version, projection_checksum, created_at, updated_at)
		VALUES ($1, 'cash', 'IDR', false, 99000, 0, 0, 0, $2, '\x00'::bytea, now(), now())`,
		accountID, newerVersion)
	require.NoError(t, err)

	// BackfillOnce processes the account. The source version from account_balances
	// is 1 (after one adjustBalance), which is < 101. The upsert WHERE clause
	// must therefore skip the update.
	require.NoError(t, runtime.BackfillOnce(ctx))

	var gotVersion int64
	err = db.QueryRowContext(ctx, `
		SELECT source_version FROM account_balances_v2 WHERE account_id = $1`, accountID,
	).Scan(&gotVersion)
	require.NoError(t, err)
	require.Equal(t, newerVersion, gotVersion,
		"backfill must not regress a v2 row whose source_version is ahead of the source")
}

// TestBackfillOnce_CheckpointResume proves that BackfillOnce picks up from the
// last processed key after a simulated worker restart, not from the beginning.
func TestBackfillOnce_CheckpointResume(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	ctx := context.Background()
	cfg := newTestConfig()
	cfg.Enabled = true
	cfg.BackfillBatchSize = 1 // one account per page so we can control pages

	runtime := NewRuntime(db, cfg, slog.Default())
	require.NoError(t, runtime.Initialize(ctx, "test"))
	repo := runtime.Controls()

	m, err := repo.GetByName(ctx, MigrationName)
	require.NoError(t, err)
	m = advanceTo(t, repo, m, migrationkit.Validated)
	m = advanceTo(t, repo, m, migrationkit.TargetReady)
	m = advanceTo(t, repo, m, migrationkit.Backfilling)
	_ = m

	// Seed two accounts.
	id1 := seedAccountWithBalance(t, db, "cash", "IDR", 10_000)
	id2 := seedAccountWithBalance(t, db, "cash", "IDR", 20_000)

	// First BackfillOnce processes one account (batchSize=1).
	require.NoError(t, runtime.BackfillOnce(ctx))

	// At most one v2 row should exist after one page.
	var count int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM account_balances_v2`).Scan(&count))
	require.Equal(t, int64(1), count, "one page should process one account")

	// Second BackfillOnce processes the remaining account.
	require.NoError(t, runtime.BackfillOnce(ctx))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM account_balances_v2`).Scan(&count))
	require.Equal(t, int64(2), count, "second page should process second account")

	// Both accounts must have v2 rows.
	for _, id := range []uuid.UUID{id1, id2} {
		var exists bool
		require.NoError(t, db.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM account_balances_v2 WHERE account_id = $1)`, id).Scan(&exists))
		require.True(t, exists, "account %s must have a v2 row after backfill", id)
	}
}

// TestBackfillOnce_CompletesAndTransitions proves that once BackfillOnce
// exhausts the keyset (empty page), it marks the checkpoint completed and
// transitions the migration to DualWriteShadow.
func TestBackfillOnce_CompletesAndTransitions(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	ctx := context.Background()
	cfg := newTestConfig()
	cfg.Enabled = true
	cfg.BackfillBatchSize = 100

	runtime := NewRuntime(db, cfg, slog.Default())
	require.NoError(t, runtime.Initialize(ctx, "test"))
	repo := runtime.Controls()

	m, err := repo.GetByName(ctx, MigrationName)
	require.NoError(t, err)
	m = advanceTo(t, repo, m, migrationkit.Validated)
	m = advanceTo(t, repo, m, migrationkit.TargetReady)
	m = advanceTo(t, repo, m, migrationkit.Backfilling)
	_ = m

	// Seed one account so backfill has something to process.
	seedAccountWithBalance(t, db, "cash", "IDR", 5_000)

	// First call: processes the one account.
	require.NoError(t, runtime.BackfillOnce(ctx))

	// Second call: empty page — checkpoint completed, migration advances.
	require.NoError(t, runtime.BackfillOnce(ctx))

	m2, err := repo.GetByName(ctx, MigrationName)
	require.NoError(t, err)
	require.Equal(t, string(migrationkit.DualWriteShadow), m2.State,
		"backfill completing an empty page must auto-transition to DualWriteShadow")
	require.NotNil(t, m2.BackfillCompletedAt, "backfill_completed_at must be set")
}

// TestReconcileOnce_DetectsTargetMissing proves that ReconcileOnce raises a
// critical mismatch when an account_balances row has no corresponding v2 row.
func TestReconcileOnce_DetectsTargetMissing(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	ctx := context.Background()
	cfg := newTestConfig()
	cfg.Enabled = true

	runtime := NewRuntime(db, cfg, slog.Default())
	require.NoError(t, runtime.Initialize(ctx, "test"))
	repo := runtime.Controls()

	m, err := repo.GetByName(ctx, MigrationName)
	require.NoError(t, err)
	// Advance to DualWriteShadow (reconciliation target state).
	m = advanceTo(t, repo, m, migrationkit.Validated)
	m = advanceTo(t, repo, m, migrationkit.TargetReady)
	m = advanceTo(t, repo, m, migrationkit.Backfilling)
	// Mark checkpoint complete so we can transition.
	_, err = db.ExecContext(ctx, `
		INSERT INTO data_migration_checkpoints
			(id, migration_id, worker_kind, partition_key, owner, status, last_processed_key, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, 'backfill', 'account_id', 'test', 'completed', '{}', now(), now())`,
		m.ID)
	require.NoError(t, err)
	m = advanceTo(t, repo, m, migrationkit.DualWriteShadow)
	_ = m

	// Seed one account with NO v2 row.
	seedAccountWithBalance(t, db, "cash", "IDR", 7_500)

	// Run reconciliation.
	require.NoError(t, runtime.ReconcileOnce(ctx))

	// A critical target_missing mismatch must have been recorded.
	m2, err := repo.GetByName(ctx, MigrationName)
	require.NoError(t, err)
	mismatches, err := repo.ListMismatches(ctx, m2.ID, "", 10, 0)
	require.NoError(t, err)
	require.NotEmpty(t, mismatches, "ReconcileOnce must record a mismatch for a missing v2 row")
	require.Equal(t, ClassificationBackfillMissing, mismatches[0].Classification)
	require.Equal(t, "critical", mismatches[0].Severity)
}

// TestReconcileOnce_MatchIsNotRecorded proves that a perfectly-matched
// source/target pair does not produce a mismatch row.
func TestReconcileOnce_MatchIsNotRecorded(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	ctx := context.Background()
	cfg := newTestConfig()
	cfg.Enabled = true

	runtime := NewRuntime(db, cfg, slog.Default())
	require.NoError(t, runtime.Initialize(ctx, "test"))
	repo := runtime.Controls()

	m, err := repo.GetByName(ctx, MigrationName)
	require.NoError(t, err)
	m = advanceTo(t, repo, m, migrationkit.Validated)
	m = advanceTo(t, repo, m, migrationkit.TargetReady)
	m = advanceTo(t, repo, m, migrationkit.Backfilling)
	_, err = db.ExecContext(ctx, `
		INSERT INTO data_migration_checkpoints
			(id, migration_id, worker_kind, partition_key, owner, status, last_processed_key, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, 'backfill', 'account_id', 'test', 'completed', '{}', now(), now())`,
		m.ID)
	require.NoError(t, err)
	m = advanceTo(t, repo, m, migrationkit.DualWriteShadow)
	_ = m

	// Seed one account and backfill it so v1=v2.
	accountID := seedAccountWithBalance(t, db, "cash", "IDR", 5_000)
	source := sourceRowFor(t, db, accountID)
	target, err := Transform(source, nil)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO account_balances_v2
			(account_id, account_type, currency, allow_negative,
			 available_amount, reserved_amount, pending_amount, restricted_amount,
			 source_version, projection_checksum, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now(), now())`,
		target.AccountID, target.AccountType, target.Currency, target.AllowNegative,
		target.AvailableAmount, target.ReservedAmount, target.PendingAmount,
		target.RestrictedAmount, target.SourceVersion, target.ProjectionChecksum)
	require.NoError(t, err)

	require.NoError(t, runtime.ReconcileOnce(ctx))

	m2, err := repo.GetByName(ctx, MigrationName)
	require.NoError(t, err)
	mismatches, err := repo.ListMismatches(ctx, m2.ID, "", 10, 0)
	require.NoError(t, err)
	require.Empty(t, mismatches, "a perfectly matched source/target pair must not produce a mismatch row")
}
