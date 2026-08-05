//go:build integration

package balancev2

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	migrationkit "github.com/herdifirdausss/seev/internal/platform/migration"
)

func newTestConfig() Config {
	return Config{BaselineCommit: "test-sha"}
}

// ensureDraftMigration calls EnsureReference and returns the migration row.
func ensureDraftMigration(t *testing.T, repo *ControlRepository) Migration {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, repo.EnsureReference(ctx, newTestConfig(), "test"))
	m, err := repo.GetByName(ctx, MigrationName)
	require.NoError(t, err)
	return m
}

// advanceTo transitions the migration from its current state to `to` using
// a fake actor/approver pair. The gate snapshot is empty (Passed=false), so
// tests targeting gated transitions should not call this for those steps.
func advanceTo(t *testing.T, repo *ControlRepository, m Migration, to migrationkit.State) Migration {
	t.Helper()
	ctx := context.Background()
	req := TransitionRequest{
		MigrationID: m.ID,
		ToState:     string(to),
		RequestedBy: "maker@test",
		ApprovedBy:  "checker@test",
		Reason:      "test advance",
	}
	result, err := repo.Transition(ctx, req, GateSnapshot{Passed: true})
	require.NoErrorf(t, err, "advance to %s", to)
	return result
}

// TestControlRepository_EnsureReference_Idempotent proves that calling
// EnsureReference twice on the same name does not error or create a second row.
func TestControlRepository_EnsureReference_Idempotent(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	repo := NewControlRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.EnsureReference(ctx, newTestConfig(), "test"))
	require.NoError(t, repo.EnsureReference(ctx, newTestConfig(), "test"), "second call must be idempotent")

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1, "only one migration row must exist")
	require.Equal(t, string(migrationkit.Draft), list[0].State)
}

// TestControlRepository_OptimisticConflict proves that a stale expectedVersion
// is rejected before any state mutation occurs.
func TestControlRepository_OptimisticConflict(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	repo := NewControlRepository(db)
	ctx := context.Background()
	m := ensureDraftMigration(t, repo)

	req := TransitionRequest{
		MigrationID:     m.ID,
		ToState:         string(migrationkit.Validated),
		RequestedBy:     "actor@test",
		Reason:          "test",
		ExpectedVersion: m.Version + 99, // stale
	}
	_, err := repo.Transition(ctx, req, GateSnapshot{Passed: true})
	require.ErrorIs(t, err, ErrOptimisticConflict)

	// Migration state must be unchanged.
	reloaded, err := repo.Get(ctx, m.ID)
	require.NoError(t, err)
	require.Equal(t, string(migrationkit.Draft), reloaded.State)
}

// TestControlRepository_SelfApprovalRejected proves that maker/checker gating
// rejects an actor approving their own dangerous transition.
func TestControlRepository_SelfApprovalRejected(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	repo := NewControlRepository(db)
	ctx := context.Background()

	m := ensureDraftMigration(t, repo)
	// Advance to RampingRead — first few steps don't require a checker.
	m = advanceTo(t, repo, m, migrationkit.Validated)
	m = advanceTo(t, repo, m, migrationkit.TargetReady)
	m = advanceTo(t, repo, m, migrationkit.Backfilling)
	// Mark backfill checkpoint complete so Backfilling→DualWriteShadow is allowed.
	_, err := db.ExecContext(ctx, `
		INSERT INTO data_migration_checkpoints
			(id, migration_id, worker_kind, partition_key, owner, status, last_processed_key, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, 'backfill', 'account_id', 'test', 'completed', '{}', now(), now())`,
		m.ID)
	require.NoError(t, err)
	m = advanceTo(t, repo, m, migrationkit.DualWriteShadow)
	m = advanceTo(t, repo, m, migrationkit.ShadowRead)
	m = advanceTo(t, repo, m, migrationkit.CanaryRead)
	m = advanceTo(t, repo, m, migrationkit.RampingRead)

	// RampingRead → SourceWriteDisabled is a dangerous transition — requires checker.
	req := TransitionRequest{
		MigrationID: m.ID,
		ToState:     string(migrationkit.SourceWriteDisabled),
		RequestedBy: "same-person@test",
		ApprovedBy:  "same-person@test", // self-approval
		Reason:      "trying to skip checker",
	}
	_, err = repo.Transition(ctx, req, GateSnapshot{Passed: true})
	require.ErrorIs(t, err, ErrApprovalRequired)
}

// TestControlRepository_MissingApprovalRejected proves that a dangerous
// transition with an empty approver is rejected.
func TestControlRepository_MissingApprovalRejected(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	repo := NewControlRepository(db)
	ctx := context.Background()

	m := ensureDraftMigration(t, repo)
	m = advanceTo(t, repo, m, migrationkit.Validated)
	m = advanceTo(t, repo, m, migrationkit.TargetReady)
	m = advanceTo(t, repo, m, migrationkit.Backfilling)
	_, err := db.ExecContext(ctx, `
		INSERT INTO data_migration_checkpoints
			(id, migration_id, worker_kind, partition_key, owner, status, last_processed_key, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, 'backfill', 'account_id', 'test', 'completed', '{}', now(), now())`,
		m.ID)
	require.NoError(t, err)
	m = advanceTo(t, repo, m, migrationkit.DualWriteShadow)
	m = advanceTo(t, repo, m, migrationkit.ShadowRead)
	m = advanceTo(t, repo, m, migrationkit.CanaryRead)
	m = advanceTo(t, repo, m, migrationkit.RampingRead)

	req := TransitionRequest{
		MigrationID: m.ID,
		ToState:     string(migrationkit.SourceWriteDisabled),
		RequestedBy: "maker@test",
		ApprovedBy:  "", // missing
		Reason:      "no approver",
	}
	_, err = repo.Transition(ctx, req, GateSnapshot{Passed: true})
	require.ErrorIs(t, err, ErrApprovalRequired)
}

// TestControlRepository_GatesSnapshot proves that Gates() reflects seeded
// mismatch rows and run rows accurately.
func TestControlRepository_GatesSnapshot(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	repo := NewControlRepository(db)
	ctx := context.Background()

	m := ensureDraftMigration(t, repo)

	// No data yet — coverage=1 (0/0 is treated as 1), but no comparisons
	// and no reconciliation run, so gate must not pass.
	gate, err := repo.Gates(ctx, m)
	require.NoError(t, err)
	assert.False(t, gate.Passed)
	assert.Equal(t, int64(0), gate.UnresolvedCritical)
	assert.InDelta(t, 1.0, gate.TargetCoverageRatio, 0.001)

	// Seed one source account (v1 balance) and one unresolved critical mismatch.
	accountID := seedAccount(t, db, "cash", "IDR")
	_, err = db.ExecContext(ctx, `
		INSERT INTO data_migration_mismatches
			(id, migration_id, resource_key_hash, resource_public_key,
			 classification, status, severity, field_mask, occurrence_count,
			 first_seen_at, last_seen_at, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, 'open', 'critical', 0, 1,
		        now(), now(), now(), now())`,
		m.ID, []byte("hash"), accountID.String(), ClassificationBackfillMissing)
	require.NoError(t, err)

	gate, err = repo.Gates(ctx, m)
	require.NoError(t, err)
	assert.False(t, gate.Passed)
	assert.Equal(t, int64(1), gate.UnresolvedCritical)
	assert.Equal(t, int64(1), gate.TargetMissingEligible)
	// 0 v2 rows vs 1 v1 row → coverage < 1.
	assert.Less(t, gate.TargetCoverageRatio, 1.0)
}

// TestControlRepository_RepairLifecycle proves the full repair path:
// create → approve → mark running (with lease) → finish → mismatch resolved.
func TestControlRepository_RepairLifecycle(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	repo := NewControlRepository(db)
	ctx := context.Background()

	m := ensureDraftMigration(t, repo)
	accountID := seedAccount(t, db, "cash", "IDR")

	// Insert a mismatch to repair.
	mismatchID := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO data_migration_mismatches
			(id, migration_id, resource_key_hash, resource_public_key,
			 classification, status, severity, field_mask, occurrence_count,
			 first_seen_at, last_seen_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'classified', 'critical', 0, 1,
		        now(), now(), now(), now())`,
		mismatchID, m.ID, []byte("hash"), accountID.String(), ClassificationBackfillMissing)
	require.NoError(t, err)

	// Create repair.
	repair, err := repo.CreateRepair(ctx, mismatchID, "maker@test", "re-backfill missing row")
	require.NoError(t, err)
	require.Equal(t, "pending_approval", repair.Status)

	// Approve repair (different actor).
	repair, err = repo.ApproveRepair(ctx, repair.ID, "checker@test", "approved")
	require.NoError(t, err)
	require.Equal(t, "approved", repair.Status)

	// Mark running — lease should be set.
	require.NoError(t, repo.MarkRepairRunning(ctx, repair.ID, "worker-1"))
	repair, err = repo.GetRepair(ctx, repair.ID)
	require.NoError(t, err)
	require.Equal(t, "running", repair.Status)
	// Verify lease was written to the DB.
	var leaseOwner *string
	err = db.QueryRowContext(ctx, `SELECT lease_owner FROM data_migration_repairs WHERE id = $1`, repair.ID).Scan(&leaseOwner)
	require.NoError(t, err)
	require.NotNil(t, leaseOwner)
	require.Equal(t, "worker-1", *leaseOwner)

	// Finish successfully.
	require.NoError(t, repo.FinishRepair(ctx, repair.ID, mismatchID, true, ""))
	repair, err = repo.GetRepair(ctx, repair.ID)
	require.NoError(t, err)
	require.Equal(t, "verified", repair.Status)
	// Lease must be cleared.
	err = db.QueryRowContext(ctx, `SELECT lease_owner FROM data_migration_repairs WHERE id = $1`, repair.ID).Scan(&leaseOwner)
	require.NoError(t, err)
	require.Nil(t, leaseOwner, "lease must be cleared after finish")

	// Mismatch must be resolved.
	mismatch, err := repo.GetMismatch(ctx, mismatchID)
	require.NoError(t, err)
	require.Equal(t, "verified", mismatch.Status)
}

// TestControlRepository_RepairIdempotency proves that creating a second repair
// for the same mismatch while one is already pending is rejected.
func TestControlRepository_RepairIdempotency(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	repo := NewControlRepository(db)
	ctx := context.Background()

	m := ensureDraftMigration(t, repo)
	accountID := seedAccount(t, db, "cash", "IDR")

	mismatchID := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO data_migration_mismatches
			(id, migration_id, resource_key_hash, resource_public_key,
			 classification, status, severity, field_mask, occurrence_count,
			 first_seen_at, last_seen_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'classified', 'critical', 0, 1,
		        now(), now(), now(), now())`,
		mismatchID, m.ID, []byte("hash2"), accountID.String(), ClassificationBackfillMissing)
	require.NoError(t, err)

	_, err = repo.CreateRepair(ctx, mismatchID, "maker@test", "first repair")
	require.NoError(t, err)

	_, err = repo.CreateRepair(ctx, mismatchID, "maker@test", "duplicate repair")
	require.Error(t, err, "creating a second repair for the same mismatch must fail")
}

// TestControlRepository_ReclaimStuckRepairs proves that a repair whose lease
// has expired is reset to pending_approval so another worker can pick it up.
func TestControlRepository_ReclaimStuckRepairs(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	repo := NewControlRepository(db)
	ctx := context.Background()

	m := ensureDraftMigration(t, repo)
	accountID := seedAccount(t, db, "cash", "IDR")

	mismatchID := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO data_migration_mismatches
			(id, migration_id, resource_key_hash, resource_public_key,
			 classification, status, severity, field_mask, occurrence_count,
			 first_seen_at, last_seen_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'classified', 'critical', 0, 1,
		        now(), now(), now(), now())`,
		mismatchID, m.ID, []byte("hash3"), accountID.String(), ClassificationBackfillMissing)
	require.NoError(t, err)

	repair, err := repo.CreateRepair(ctx, mismatchID, "maker@test", "repair")
	require.NoError(t, err)
	repair, err = repo.ApproveRepair(ctx, repair.ID, "checker@test", "ok")
	require.NoError(t, err)
	require.NoError(t, repo.MarkRepairRunning(ctx, repair.ID, "crashed-worker"))

	// Simulate an already-expired lease.
	_, err = db.ExecContext(ctx, `
		UPDATE data_migration_repairs SET lease_expires_at = now() - interval '1 second'
		WHERE id = $1`, repair.ID)
	require.NoError(t, err)

	reclaimed, err := repo.ReclaimStuckRepairs(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), reclaimed)

	repair, err = repo.GetRepair(ctx, repair.ID)
	require.NoError(t, err)
	require.Equal(t, "pending_approval", repair.Status, "reclaimed repair must be open for re-approval")
	// Verify lease is cleared.
	var reclaimedLease *string
	err = db.QueryRowContext(ctx, `SELECT lease_owner FROM data_migration_repairs WHERE id = $1`, repair.ID).Scan(&reclaimedLease)
	require.NoError(t, err)
	require.Nil(t, reclaimedLease, "lease must be cleared after reclaim")

	// Corresponding mismatch must be reset to repair_pending.
	mismatch, err := repo.GetMismatch(ctx, mismatchID)
	require.NoError(t, err)
	require.Equal(t, "repair_pending", mismatch.Status)
}

// TestControlRepository_SetReadPercentage_AboveThresholdRequiresChecker proves
// that raising read percentage above 25% requires a different approver.
func TestControlRepository_SetReadPercentage_AboveThresholdRequiresChecker(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	repo := NewControlRepository(db)
	ctx := context.Background()

	m := ensureDraftMigration(t, repo)
	m = advanceTo(t, repo, m, migrationkit.Validated)
	m = advanceTo(t, repo, m, migrationkit.TargetReady)
	m = advanceTo(t, repo, m, migrationkit.Backfilling)
	_, err := db.ExecContext(ctx, `
		INSERT INTO data_migration_checkpoints
			(id, migration_id, worker_kind, partition_key, owner, status, last_processed_key, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, 'backfill', 'account_id', 'test', 'completed', '{}', now(), now())`,
		m.ID)
	require.NoError(t, err)
	m = advanceTo(t, repo, m, migrationkit.DualWriteShadow)
	m = advanceTo(t, repo, m, migrationkit.ShadowRead)
	m = advanceTo(t, repo, m, migrationkit.CanaryRead)
	m = advanceTo(t, repo, m, migrationkit.RampingRead)

	// 25% → same actor is allowed.
	m, err = repo.SetReadPercentage(ctx, m.ID, 2500, "maker@test", "maker@test", "test ramp", m.Version, GateSnapshot{Passed: true})
	require.NoError(t, err, "25%% should not require a checker")
	require.Equal(t, 2500, m.ReadPercentageBasisPoints)

	// >25% → must reject self-approval.
	_, err = repo.SetReadPercentage(ctx, m.ID, 2501, "maker@test", "maker@test", "test ramp above 25", m.Version, GateSnapshot{Passed: true})
	require.ErrorIs(t, err, ErrApprovalRequired, "above 25%% must require a different approver")

	// >25% → different approver must work.
	m, err = repo.SetReadPercentage(ctx, m.ID, 2501, "maker@test", "checker@test", "test ramp above 25 with checker", m.Version, GateSnapshot{Passed: true})
	require.NoError(t, err)
	require.Equal(t, 2501, m.ReadPercentageBasisPoints)
}

// TestControlRepository_AcquireCheckpoint_LeaseExclusivity proves that a
// second acquire attempt for the same (migration, kind, partition) while a
// valid lease is held does not hand out a second lease.
func TestControlRepository_AcquireCheckpoint_LeaseExclusivity(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	repo := NewControlRepository(db)
	ctx := context.Background()

	m := ensureDraftMigration(t, repo)

	cp1, ok1, err := repo.AcquireCheckpoint(ctx, m.ID, "backfill", "account_id", "worker-1", time.Minute)
	require.NoError(t, err)
	require.True(t, ok1, "first acquire must succeed")
	require.Equal(t, "worker-1", cp1.LeaseOwner)

	_, ok2, err := repo.AcquireCheckpoint(ctx, m.ID, "backfill", "account_id", "worker-2", time.Minute)
	require.NoError(t, err)
	require.False(t, ok2, "second acquire on an active lease must be denied")
}

// TestControlRepository_AcquireCheckpoint_ReclaimsExpiredLease proves that an
// expired lease can be taken over by a different worker.
func TestControlRepository_AcquireCheckpoint_ReclaimsExpiredLease(t *testing.T) {
	db := setupBalanceV2TestDB(t)
	repo := NewControlRepository(db)
	ctx := context.Background()

	m := ensureDraftMigration(t, repo)

	cp1, ok1, err := repo.AcquireCheckpoint(ctx, m.ID, "backfill", "account_id", "worker-1", time.Minute)
	require.NoError(t, err)
	require.True(t, ok1)

	// Expire the lease directly.
	_, err = db.ExecContext(ctx, `
		UPDATE data_migration_checkpoints
		SET lease_expires_at = now() - interval '1 second'
		WHERE id = $1`, cp1.ID)
	require.NoError(t, err)

	_, ok2, err := repo.AcquireCheckpoint(ctx, m.ID, "backfill", "account_id", "worker-2", time.Minute)
	require.NoError(t, err)
	require.True(t, ok2, "expired lease must be reclaimable by another worker")
}
