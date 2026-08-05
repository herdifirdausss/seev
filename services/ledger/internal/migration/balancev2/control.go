package balancev2

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/platform/migration"
	"github.com/herdifirdausss/seev/internal/platform/database"
)

var (
	ErrMigrationNotFound  = errors.New("balancev2: migration not found")
	ErrOptimisticConflict = errors.New("balancev2: migration changed since it was read")
	ErrGateBlocked        = errors.New("balancev2: migration gate is not satisfied")
	ErrApprovalRequired   = errors.New("balancev2: checker approval is required")
	ErrNoActiveMigration  = errors.New("balancev2: no active migration")
)

// repairLeaseDuration bounds how long a single approve-and-execute repair
// (§13.4/§18.5) may hold the 'running' status before ReclaimStuckRepairs
// treats it as abandoned by a crashed process, mirroring the checkpoint
// lease pattern in AcquireCheckpoint.
const repairLeaseDuration = 5 * time.Minute

const migrationColumns = `
	id, public_id, name, resource, source_version, target_version, state,
	previous_state, read_percentage_basis_points, shadow_percentage_basis_points,
	strict_dual_write, source_fallback_enabled, source_write_enabled,
	target_write_enabled, transform_version, baseline_commit, created_by,
	updated_by, pause_reason, failure_code, started_at, backfill_completed_at,
	cutover_started_at, completed_at, created_at, updated_at, version`

type ControlRepository struct {
	db database.DatabaseSQL
}

func NewControlRepository(db database.DatabaseSQL) *ControlRepository {
	return &ControlRepository{db: db}
}

func (r *ControlRepository) EnsureReference(ctx context.Context, cfg Config, actor string) error {
	if strings.TrimSpace(actor) == "" {
		actor = "service:ledger"
	}
	id := uuid.New()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO data_migrations (
			id, public_id, name, resource, source_version, target_version, state,
			read_percentage_basis_points, shadow_percentage_basis_points,
			strict_dual_write, source_fallback_enabled, source_write_enabled,
			target_write_enabled, transform_version, baseline_commit, created_by,
			updated_by, created_at, updated_at, version
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'draft', 0, 0, false, true, true,
			false, $7, $8, $9, $9, now(), now(), 1)
		ON CONFLICT (name) DO NOTHING`,
		id, MigrationName, MigrationName, Resource, SourceVersion, TargetVersion,
		TransformVersion, cfg.BaselineCommit, actor)
	if err != nil {
		return fmt.Errorf("balancev2: ensure migration: %w", err)
	}
	return nil
}

func (r *ControlRepository) Get(ctx context.Context, id uuid.UUID) (Migration, error) {
	return r.get(ctx, `SELECT `+migrationColumns+` FROM data_migrations WHERE id = $1`, id)
}

func (r *ControlRepository) GetByName(ctx context.Context, name string) (Migration, error) {
	return r.get(ctx, `SELECT `+migrationColumns+` FROM data_migrations WHERE name = $1`, name)
}

func (r *ControlRepository) List(ctx context.Context) ([]Migration, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+migrationColumns+` FROM data_migrations ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("balancev2: list migrations: %w", err)
	}
	defer rows.Close()
	items := make([]Migration, 0)
	for rows.Next() {
		item, scanErr := scanMigration(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("balancev2: list migrations rows: %w", err)
	}
	return items, nil
}

func (r *ControlRepository) get(ctx context.Context, query string, args ...any) (Migration, error) {
	item, err := scanMigration(r.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Migration{}, ErrMigrationNotFound
	}
	if err != nil {
		return Migration{}, fmt.Errorf("balancev2: get migration: %w", err)
	}
	return item, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMigration(row rowScanner) (Migration, error) {
	var item Migration
	var previousState, pauseReason, failureCode sql.NullString
	var startedAt, backfillAt, cutoverAt, completedAt sql.NullTime
	err := row.Scan(
		&item.ID, &item.PublicID, &item.Name, &item.Resource, &item.SourceVersion,
		&item.TargetVersion, &item.State, &previousState,
		&item.ReadPercentageBasisPoints, &item.ShadowPercentageBasisPoints,
		&item.StrictDualWrite, &item.SourceFallbackEnabled,
		&item.SourceWriteEnabled, &item.TargetWriteEnabled, &item.TransformVersion,
		&item.BaselineCommit, &item.CreatedBy, &item.UpdatedBy, &pauseReason,
		&failureCode, &startedAt, &backfillAt, &cutoverAt, &completedAt,
		&item.CreatedAt, &item.UpdatedAt, &item.Version,
	)
	if err != nil {
		return Migration{}, err
	}
	item.PreviousState = previousState.String
	item.PauseReason = pauseReason.String
	item.FailureCode = failureCode.String
	item.StartedAt = nullTimePtr(startedAt)
	item.BackfillCompletedAt = nullTimePtr(backfillAt)
	item.CutoverStartedAt = nullTimePtr(cutoverAt)
	item.CompletedAt = nullTimePtr(completedAt)
	return item, nil
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	copy := value.Time
	return &copy
}

type TransitionRequest struct {
	MigrationID     uuid.UUID
	ToState         string
	RequestedBy     string
	ApprovedBy      string
	Reason          string
	ExpectedVersion int64
}

func (r *ControlRepository) Transition(ctx context.Context, req TransitionRequest, gate GateSnapshot) (Migration, error) {
	if strings.TrimSpace(req.RequestedBy) == "" || strings.TrimSpace(req.Reason) == "" {
		return Migration{}, fmt.Errorf("balancev2: actor and reason are required")
	}
	to := migrationkit.State(req.ToState)
	if !migrationkit.IsActive(to) && to != migrationkit.Completed && to != migrationkit.RolledBack && to != migrationkit.CancelledBeforeWrite {
		return Migration{}, fmt.Errorf("balancev2: unsupported target state %q", req.ToState)
	}
	var result Migration
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		current, err := scanMigration(tx.QueryRowContext(ctx, `SELECT `+migrationColumns+` FROM data_migrations WHERE id = $1 FOR UPDATE`, req.MigrationID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMigrationNotFound
		}
		if err != nil {
			return fmt.Errorf("balancev2: lock migration: %w", err)
		}
		if req.ExpectedVersion != 0 && req.ExpectedVersion != current.Version {
			return ErrOptimisticConflict
		}
		if err := migrationkit.ValidateTransition(migrationkit.State(current.State), to); err != nil {
			return err
		}
		if current.State == string(migrationkit.Paused) && to != migrationkit.RollingBack && to != migrationkit.Failed && to != migrationkit.State(current.PreviousState) {
			return fmt.Errorf("%w: paused migration can only resume its previous state", migrationkit.ErrInvalidTransition)
		}
		if to == migrationkit.TargetPrimary && current.ReadPercentageBasisPoints < migrationkit.BasisPoints {
			return fmt.Errorf("balancev2: target primary requires 100%% target reads")
		}
		if current.State == string(migrationkit.Backfilling) && to == migrationkit.DualWriteShadow {
			var checkpointStatus string
			checkpointErr := tx.QueryRowContext(ctx, `
				SELECT status FROM data_migration_checkpoints
				WHERE migration_id = $1 AND worker_kind = 'backfill' AND partition_key = 'account_id'`, current.ID).Scan(&checkpointStatus)
			if checkpointErr != nil && !errors.Is(checkpointErr, sql.ErrNoRows) {
				return fmt.Errorf("balancev2: read backfill checkpoint: %w", checkpointErr)
			}
			if errors.Is(checkpointErr, sql.ErrNoRows) || checkpointStatus != "completed" {
				return fmt.Errorf("%w: backfill checkpoint is not complete", ErrGateBlocked)
			}
		}
		if migrationkit.RequiresChecker(migrationkit.State(current.State), to, current.ReadPercentageBasisPoints) {
			if strings.TrimSpace(req.ApprovedBy) == "" || req.ApprovedBy == req.RequestedBy {
				return ErrApprovalRequired
			}
		}
		if requiresGate(to) && !gate.Passed {
			return fmt.Errorf("%w: %s", ErrGateBlocked, gate.Reason)
		}

		args := []any{string(to), req.RequestedBy, req.MigrationID}
		set := `state = $1, updated_by = $2, updated_at = now(), version = version + 1`
		if to == migrationkit.Paused {
			set += `, previous_state = state, pause_reason = $4`
			args = append(args, req.Reason)
		}
		if to == migrationkit.RollingBack {
			set += `, read_percentage_basis_points = 0, source_fallback_enabled = true,
				source_write_enabled = true, target_write_enabled = false, strict_dual_write = false`
		}
		if to == migrationkit.DualWriteShadow {
			set += `, target_write_enabled = true, strict_dual_write = false`
		}
		if to == migrationkit.Backfilling {
			set += `, target_write_enabled = true, strict_dual_write = false`
		}
		if to == migrationkit.ShadowRead {
			set += `, target_write_enabled = true, strict_dual_write = true,
				shadow_percentage_basis_points = 10000`
		}
		if to == migrationkit.CanaryRead || to == migrationkit.RampingRead || to == migrationkit.TargetPrimary {
			set += `, target_write_enabled = true, strict_dual_write = true, source_fallback_enabled = true`
		}
		if to == migrationkit.SourceWriteDisabled {
			set += `, source_write_enabled = false, target_write_enabled = true, strict_dual_write = true`
		}
		if to == migrationkit.Observation {
			set += `, target_write_enabled = true, strict_dual_write = true, source_fallback_enabled = true`
		}
		if to == migrationkit.Completed {
			set += `, completed_at = now(), read_percentage_basis_points = 10000, source_fallback_enabled = true, target_write_enabled = true, strict_dual_write = true`
		}
		if to == migrationkit.Validated || to == migrationkit.TargetReady || to == migrationkit.Backfilling || to == migrationkit.DualWriteShadow {
			set += `, started_at = COALESCE(started_at, now())`
		}
		if to == migrationkit.CanaryRead || to == migrationkit.RampingRead || to == migrationkit.TargetPrimary {
			set += `, cutover_started_at = COALESCE(cutover_started_at, now())`
		}
		if current.State == string(migrationkit.Backfilling) && to == migrationkit.DualWriteShadow {
			set += `, backfill_completed_at = now()`
		}
		if to == migrationkit.Failed {
			set += `, failure_code = $4, read_percentage_basis_points = 0,
				source_fallback_enabled = true, source_write_enabled = true,
				target_write_enabled = false, strict_dual_write = false`
			args = append(args, "operator_transition")
		}
		if to != migrationkit.Paused && current.State == string(migrationkit.Paused) {
			set += `, previous_state = NULL, pause_reason = NULL`
		}
		if _, err := tx.ExecContext(ctx, `UPDATE data_migrations SET `+set+` WHERE id = $3`, args...); err != nil {
			return fmt.Errorf("balancev2: update migration state: %w", err)
		}
		if err := insertTransition(ctx, tx, current, req, gate); err != nil {
			return err
		}
		result, err = scanMigration(tx.QueryRowContext(ctx, `SELECT `+migrationColumns+` FROM data_migrations WHERE id = $1`, req.MigrationID))
		return err
	})
	if err != nil {
		return Migration{}, err
	}
	return result, nil
}

func requiresGate(state migrationkit.State) bool {
	return migrationkit.RequiresGate(state)
}

func insertTransition(ctx context.Context, tx *sql.Tx, current Migration, req TransitionRequest, gate GateSnapshot) error {
	snapshot, err := json.Marshal(gate)
	if err != nil {
		return fmt.Errorf("balancev2: encode transition evidence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO data_migration_transitions
			(id, migration_id, from_state, to_state, requested_by, approved_by, reason, evidence_snapshot, created_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8::jsonb, now())`,
		uuid.New(), current.ID, current.State, req.ToState, req.RequestedBy,
		req.ApprovedBy, req.Reason, snapshot); err != nil {
		return fmt.Errorf("balancev2: record transition: %w", err)
	}
	return nil
}

func (r *ControlRepository) SetReadPercentage(ctx context.Context, id uuid.UUID, percentage int, actor, approver, reason string, expectedVersion int64, gate GateSnapshot) (Migration, error) {
	if percentage < 0 || percentage > 10000 {
		return Migration{}, fmt.Errorf("balancev2: read percentage must be between 0 and 10000 basis points")
	}
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return Migration{}, fmt.Errorf("balancev2: actor and reason are required")
	}
	var result Migration
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		current, err := scanMigration(tx.QueryRowContext(ctx, `SELECT `+migrationColumns+` FROM data_migrations WHERE id = $1 FOR UPDATE`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMigrationNotFound
		}
		if err != nil {
			return err
		}
		if expectedVersion != 0 && expectedVersion != current.Version {
			return ErrOptimisticConflict
		}
		if percentage > 0 {
			switch current.State {
			case string(migrationkit.CanaryRead), string(migrationkit.RampingRead),
				string(migrationkit.TargetPrimary), string(migrationkit.SourceWriteDisabled),
				string(migrationkit.Observation), string(migrationkit.Completed):
			default:
				return fmt.Errorf("balancev2: target reads are not enabled in state %s", current.State)
			}
		}
		if percentage > current.ReadPercentageBasisPoints && percentage > 2500 {
			if strings.TrimSpace(approver) == "" || approver == actor {
				return ErrApprovalRequired
			}
		}
		if percentage > current.ReadPercentageBasisPoints && percentage > 0 && !gate.Passed {
			return fmt.Errorf("%w: %s", ErrGateBlocked, gate.Reason)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE data_migrations
			SET read_percentage_basis_points = $1, source_fallback_enabled = true,
				updated_by = $2, updated_at = now(), version = version + 1
			WHERE id = $3`, percentage, actor, id); err != nil {
			return fmt.Errorf("balancev2: set read percentage: %w", err)
		}
		snapshot, marshalErr := json.Marshal(gate)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO data_migration_transitions
				(id, migration_id, from_state, to_state, requested_by, approved_by, reason, evidence_snapshot, created_at)
			VALUES ($1, $2, $3, $3, $4, NULLIF($5, ''), $6, $7::jsonb, now())`,
			uuid.New(), current.ID, current.State, actor, approver, reason, snapshot); err != nil {
			return fmt.Errorf("balancev2: record read rollout: %w", err)
		}
		result, err = scanMigration(tx.QueryRowContext(ctx, `SELECT `+migrationColumns+` FROM data_migrations WHERE id = $1`, id))
		return err
	})
	return result, err
}

func (r *ControlRepository) SetDualWrite(ctx context.Context, id uuid.UUID, strict bool, actor, reason string, expectedVersion int64) (Migration, error) {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return Migration{}, fmt.Errorf("balancev2: actor and reason are required")
	}
	var result Migration
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		current, err := scanMigration(tx.QueryRowContext(ctx, `SELECT `+migrationColumns+` FROM data_migrations WHERE id = $1 FOR UPDATE`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMigrationNotFound
		}
		if err != nil {
			return err
		}
		if expectedVersion != 0 && expectedVersion != current.Version {
			return ErrOptimisticConflict
		}
		if !targetWriteStage(current) {
			return fmt.Errorf("balancev2: dual writes are not enabled in state %s", current.State)
		}
		if !strict && current.State != string(migrationkit.DualWriteShadow) {
			return fmt.Errorf("balancev2: non-strict dual writes are only allowed during shadow dual write")
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE data_migrations
			SET strict_dual_write = $1, target_write_enabled = true,
				updated_by = $2, updated_at = now(), version = version + 1
			WHERE id = $3`, strict, actor, id); err != nil {
			return fmt.Errorf("balancev2: set dual write: %w", err)
		}
		gate := GateSnapshot{Passed: true, FreshAt: time.Now().UTC(), Reason: "mode change"}
		if err := insertTransition(ctx, tx, current, TransitionRequest{MigrationID: id, ToState: current.State, RequestedBy: actor, Reason: reason}, gate); err != nil {
			return err
		}
		result, err = scanMigration(tx.QueryRowContext(ctx, `SELECT `+migrationColumns+` FROM data_migrations WHERE id = $1`, id))
		return err
	})
	return result, err
}

func (r *ControlRepository) Pause(ctx context.Context, id uuid.UUID, actor, reason string, expectedVersion int64) (Migration, error) {
	return r.Transition(ctx, TransitionRequest{MigrationID: id, ToState: string(migrationkit.Paused), RequestedBy: actor, Reason: reason, ExpectedVersion: expectedVersion}, GateSnapshot{Passed: true, FreshAt: time.Now().UTC(), Reason: "pause"})
}

func (r *ControlRepository) Resume(ctx context.Context, id uuid.UUID, actor, reason string, expectedVersion int64) (Migration, error) {
	current, err := r.Get(ctx, id)
	if err != nil {
		return Migration{}, err
	}
	if current.State != string(migrationkit.Paused) || current.PreviousState == "" {
		return Migration{}, fmt.Errorf("balancev2: migration is not resumable")
	}
	return r.Transition(ctx, TransitionRequest{MigrationID: id, ToState: current.PreviousState, RequestedBy: actor, Reason: reason, ExpectedVersion: expectedVersion}, GateSnapshot{Passed: true, FreshAt: time.Now().UTC(), Reason: "resume"})
}

// AcquireCheckpoint claims a durable keyset checkpoint. Expired leases are
// stealable, while a live lease can only be renewed by its current owner.
// This keeps a restarted worker from duplicating an unbounded range and makes
// worker ownership visible to operators.
func (r *ControlRepository) AcquireCheckpoint(ctx context.Context, migrationID uuid.UUID, workerKind, partitionKey, owner string, lease time.Duration) (Checkpoint, bool, error) {
	if lease <= 0 {
		lease = time.Minute
	}
	if strings.TrimSpace(workerKind) == "" || strings.TrimSpace(partitionKey) == "" || strings.TrimSpace(owner) == "" {
		return Checkpoint{}, false, fmt.Errorf("balancev2: checkpoint worker, partition, and owner are required")
	}
	var checkpoint Checkpoint
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			INSERT INTO data_migration_checkpoints (
				id, migration_id, worker_kind, partition_key, status,
				lease_owner, lease_expires_at, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, 'running', $5, now() + $6::interval, now(), now())
			ON CONFLICT (migration_id, worker_kind, partition_key) DO UPDATE SET
				lease_owner = EXCLUDED.lease_owner,
				lease_expires_at = EXCLUDED.lease_expires_at,
				status = 'running',
				updated_at = now()
			WHERE data_migration_checkpoints.lease_expires_at < now()
			   OR data_migration_checkpoints.lease_owner = EXCLUDED.lease_owner
			RETURNING id, migration_id, worker_kind, partition_key,
				last_source_key, watermark_version, processed_count, updated_count,
				skipped_count, failed_count, lease_owner, lease_expires_at, status,
				created_at, updated_at`,
			uuid.New(), migrationID, workerKind, partitionKey, owner, intervalLiteral(lease))
		if err := scanCheckpoint(row, &checkpoint); errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return fmt.Errorf("balancev2: acquire checkpoint: %w", err)
		}
		return nil
	})
	if err != nil {
		return Checkpoint{}, false, err
	}
	return checkpoint, checkpoint.ID != uuid.Nil, nil
}

func intervalLiteral(value time.Duration) string {
	return fmt.Sprintf("%f seconds", value.Seconds())
}

func (r *ControlRepository) SaveCheckpoint(ctx context.Context, checkpoint Checkpoint) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE data_migration_checkpoints
		SET last_source_key = NULLIF($1, ''), watermark_version = $2,
			processed_count = $3, updated_count = $4, skipped_count = $5,
			failed_count = $6, lease_owner = $7, lease_expires_at = $8,
			status = $9, updated_at = now()
		WHERE id = $10 AND migration_id = $11 AND lease_owner = $12`,
		checkpoint.LastSourceKey, checkpoint.WatermarkVersion,
		checkpoint.ProcessedCount, checkpoint.UpdatedCount, checkpoint.SkippedCount,
		checkpoint.FailedCount, checkpoint.LeaseOwner, checkpoint.LeaseExpiresAt,
		checkpoint.Status, checkpoint.ID, checkpoint.MigrationID, checkpoint.LeaseOwner)
	if err != nil {
		return fmt.Errorf("balancev2: save checkpoint: %w", err)
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil {
		return fmt.Errorf("balancev2: checkpoint rows affected: %w", affectedErr)
	} else if affected != 1 {
		return fmt.Errorf("balancev2: checkpoint lease is no longer owned")
	}
	return nil
}

func (r *ControlRepository) SaveCheckpointTx(ctx context.Context, tx *sql.Tx, checkpoint Checkpoint) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE data_migration_checkpoints
		SET last_source_key = NULLIF($1, ''), watermark_version = $2,
			processed_count = $3, updated_count = $4, skipped_count = $5,
			failed_count = $6, lease_owner = $7, lease_expires_at = $8,
			status = $9, updated_at = now()
		WHERE id = $10 AND migration_id = $11 AND lease_owner = $12`,
		checkpoint.LastSourceKey, checkpoint.WatermarkVersion,
		checkpoint.ProcessedCount, checkpoint.UpdatedCount, checkpoint.SkippedCount,
		checkpoint.FailedCount, checkpoint.LeaseOwner, checkpoint.LeaseExpiresAt,
		checkpoint.Status, checkpoint.ID, checkpoint.MigrationID, checkpoint.LeaseOwner)
	if err != nil {
		return fmt.Errorf("balancev2: save checkpoint in transaction: %w", err)
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil {
		return fmt.Errorf("balancev2: checkpoint rows affected in transaction: %w", affectedErr)
	} else if affected != 1 {
		return fmt.Errorf("balancev2: checkpoint lease is no longer owned in transaction")
	}
	return nil
}

func scanCheckpoint(row rowScanner, checkpoint *Checkpoint) error {
	var lastKey sql.NullString
	var watermark sql.NullInt64
	var leaseOwner sql.NullString
	var leaseExpires sql.NullTime
	if err := row.Scan(
		&checkpoint.ID, &checkpoint.MigrationID, &checkpoint.WorkerKind,
		&checkpoint.PartitionKey, &lastKey, &watermark, &checkpoint.ProcessedCount,
		&checkpoint.UpdatedCount, &checkpoint.SkippedCount, &checkpoint.FailedCount,
		&leaseOwner, &leaseExpires, &checkpoint.Status, &checkpoint.CreatedAt,
		&checkpoint.UpdatedAt); err != nil {
		return err
	}
	checkpoint.LastSourceKey = lastKey.String
	if watermark.Valid {
		value := watermark.Int64
		checkpoint.WatermarkVersion = &value
	}
	checkpoint.LeaseOwner = leaseOwner.String
	if leaseExpires.Valid {
		value := leaseExpires.Time
		checkpoint.LeaseExpiresAt = &value
	}
	return nil
}

func resourceKeyHash(accountID uuid.UUID, layer string) []byte {
	hash := sha256.Sum256([]byte(layer + ":" + accountID.String()))
	return hash[:]
}

func (r *ControlRepository) RecordComparison(ctx context.Context, migrationID uuid.UUID, comparison Comparison) error {
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return r.recordComparisonTx(ctx, tx, migrationID, comparison)
	})
}

func (r *ControlRepository) recordComparisonTx(ctx context.Context, tx *sql.Tx, migrationID uuid.UUID, comparison Comparison) error {
	layer := comparison.ResourceLayer
	if layer == "" {
		layer = "source_target"
	}
	keyHash := resourceKeyHash(comparison.AccountID, layer)
	if comparison.Result == "match" {
		_, err := tx.ExecContext(ctx, `
			UPDATE data_migration_mismatches
			SET status = 'verified', last_seen_at = now(), updated_at = now(),
				occurrence_count = occurrence_count + 1, last_error_code = NULL
			WHERE migration_id = $1 AND resource_key_hash = $2
				AND status NOT IN ('ignored_with_reason')`, migrationID, keyHash)
		if err != nil {
			return fmt.Errorf("balancev2: verify comparison: %w", err)
		}
		return nil
	}
	status := MismatchOpen
	if comparison.Severity == "warning" {
		status = MismatchClassified
	}
	var sourceVersion, targetVersion any = comparison.SourceVersion, nil
	if comparison.TargetVersionSet || comparison.TargetVersion > 0 {
		targetVersion = comparison.TargetVersion
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO data_migration_mismatches (
			id, migration_id, resource_key_hash, resource_public_key,
			classification, status, severity, field_mask, source_version,
			target_version, source_checksum, target_checksum, occurrence_count,
			first_seen_at, last_seen_at, repair_attempt_count, last_error_code,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 1,
			now(), now(), 0, NULLIF($13, ''), now(), now())
		ON CONFLICT (migration_id, resource_key_hash) DO UPDATE SET
			classification = EXCLUDED.classification,
			status = CASE WHEN data_migration_mismatches.status = 'ignored_with_reason'
				THEN data_migration_mismatches.status ELSE EXCLUDED.status END,
			severity = EXCLUDED.severity, field_mask = EXCLUDED.field_mask,
			source_version = EXCLUDED.source_version,
			target_version = EXCLUDED.target_version,
			source_checksum = EXCLUDED.source_checksum,
			target_checksum = EXCLUDED.target_checksum,
			occurrence_count = data_migration_mismatches.occurrence_count + 1,
			last_seen_at = now(), last_error_code = EXCLUDED.last_error_code,
			updated_at = now()`,
		uuid.New(), migrationID, keyHash, "sha256:ledger-account:"+layer,
		comparison.Classification, status, comparison.Severity, comparison.FieldMask,
		sourceVersion, targetVersion, comparison.SourceChecksum, comparison.TargetChecksum,
		comparison.ErrorCode)
	if err != nil {
		return fmt.Errorf("balancev2: record comparison: %w", err)
	}
	// Shadow write gaps are recorded from inside the posting transaction after
	// its target savepoint is rolled back. That transaction holds FOR SHARE on
	// the migration control row, so an auto-abort UPDATE here would deadlock
	// against the still-open source posting. Strict mode never reaches this
	// path; reconciliation and shadow-read critical mismatches still auto-abort.
	if comparison.Severity == "critical" && comparison.Result != "write_gap" {
		abortResult, err := tx.ExecContext(ctx, `
			UPDATE data_migrations
			SET read_percentage_basis_points = 0,
				source_fallback_enabled = true,
				updated_by = 'worker:balancev2:auto-abort',
				updated_at = now(), version = version + 1
		WHERE id = $1 AND read_percentage_basis_points > 0`, migrationID)
		if err != nil {
			return fmt.Errorf("balancev2: auto-abort target reads: %w", err)
		} else if affected, affectedErr := abortResult.RowsAffected(); affectedErr != nil {
			return fmt.Errorf("balancev2: auto-abort rows affected: %w", affectedErr)
		} else if affected > 0 {
			var state string
			if err := tx.QueryRowContext(ctx, `SELECT state FROM data_migrations WHERE id = $1`, migrationID).Scan(&state); err != nil {
				return fmt.Errorf("balancev2: read auto-aborted migration state: %w", err)
			}
			evidence, marshalErr := json.Marshal(map[string]any{
				"reason":         "critical mismatch",
				"resource_layer": comparison.ResourceLayer,
				"classification": comparison.Classification,
				"field_mask":     comparison.FieldMask,
				"source_version": comparison.SourceVersion,
				"target_version": comparison.TargetVersion,
			})
			if marshalErr != nil {
				return fmt.Errorf("balancev2: encode auto-abort evidence: %w", marshalErr)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO data_migration_transitions
					(id, migration_id, from_state, to_state, requested_by, reason, evidence_snapshot, created_at)
				VALUES ($1, $2, $3, $3, 'worker:balancev2:auto-abort', 'critical mismatch', $4::jsonb, now())`,
				uuid.New(), migrationID, state, evidence); err != nil {
				return fmt.Errorf("balancev2: record auto-abort: %w", err)
			}
		}
	}
	mismatchesTotal.WithLabelValues(MigrationName, comparison.Classification, status).Inc()
	return nil
}

func boundedErrorCode(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		return value[:128]
	}
	return value
}

func (r *ControlRepository) IsBlocked(ctx context.Context, migrationID, accountID uuid.UUID) (bool, error) {
	hashes := [][]byte{
		resourceKeyHash(accountID, "source_target"),
		resourceKeyHash(accountID, "live_write"),
		resourceKeyHash(accountID, "source_ledger"),
		resourceKeyHash(accountID, "target_ledger"),
	}
	var blocked bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM data_migration_mismatches
			WHERE migration_id = $1 AND resource_key_hash IN ($2, $3, $4, $5)
			  AND severity = 'critical'
			  AND status IN ('open', 'classified', 'repair_pending', 'repairing', 'blocked')
		)`, migrationID, hashes[0], hashes[1], hashes[2], hashes[3]).Scan(&blocked)
	if err != nil {
		return false, fmt.Errorf("balancev2: check account mismatch block: %w", err)
	}
	return blocked, nil
}

func (r *ControlRepository) CreateRun(ctx context.Context, migrationID uuid.UUID, runType, sourceCutoff, actor string) (Run, error) {
	if strings.TrimSpace(runType) == "" {
		return Run{}, fmt.Errorf("balancev2: run type is required")
	}
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO data_migration_runs (
			id, migration_id, run_type, status, started_at, source_cutoff,
			evidence, created_at
		)
		VALUES ($1, $2, $3, 'running', now(), NULLIF($4, ''),
			jsonb_build_object('started_by', $5), now())
		RETURNING `+runColumns,
		uuid.New(), migrationID, runType, sourceCutoff, actor)
	var run Run
	err := scanRunRow(row, &run)
	if err != nil {
		return Run{}, fmt.Errorf("balancev2: create migration run: %w", err)
	}
	return run, nil
}

const runColumns = `id, migration_id, run_type, status, started_at, finished_at,
	source_cutoff, target_cutoff, processed_count, match_count, mismatch_count,
	error_count, evidence, created_at`

func scanRunRow(row rowScanner, run *Run) error {
	var finishedAt sql.NullTime
	var sourceCutoff, targetCutoff sql.NullString
	var evidence []byte
	if err := row.Scan(&run.ID, &run.MigrationID, &run.RunType, &run.Status,
		&run.StartedAt, &finishedAt, &sourceCutoff, &targetCutoff,
		&run.ProcessedCount, &run.MatchCount, &run.MismatchCount,
		&run.ErrorCount, &evidence, &run.CreatedAt); err != nil {
		return err
	}
	run.SourceCutoff = sourceCutoff.String
	run.TargetCutoff = targetCutoff.String
	if finishedAt.Valid {
		value := finishedAt.Time
		run.FinishedAt = &value
	}
	if len(evidence) > 0 {
		_ = json.Unmarshal(evidence, &run.Evidence)
	}
	return nil
}

func (r *ControlRepository) FinishRun(ctx context.Context, runID uuid.UUID, status string, processed, matches, mismatches, errorsCount int64, evidence map[string]any) error {
	payload, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("balancev2: encode run evidence: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE data_migration_runs
		SET status = $1, finished_at = now(), processed_count = $2,
			match_count = $3, mismatch_count = $4, error_count = $5,
			evidence = COALESCE(evidence, '{}'::jsonb) || $6::jsonb
		WHERE id = $7`, status, processed, matches, mismatches, errorsCount, payload, runID)
	if err != nil {
		return fmt.Errorf("balancev2: finish migration run: %w", err)
	}
	return nil
}

func (r *ControlRepository) Gates(ctx context.Context, migration Migration) (GateSnapshot, error) {
	var sourceCount, targetCount, critical, targetMissing int64
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM account_balances`).Scan(&sourceCount); err != nil {
		return GateSnapshot{}, fmt.Errorf("balancev2: gate source coverage: %w", err)
	}
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM account_balances_v2`).Scan(&targetCount); err != nil {
		return GateSnapshot{}, fmt.Errorf("balancev2: gate target coverage: %w", err)
	}
	if err := r.db.QueryRowContext(ctx, `
		SELECT count(*) FROM data_migration_mismatches
		WHERE migration_id = $1 AND severity = 'critical'
		  AND status IN ('open', 'classified', 'repair_pending', 'repairing', 'blocked')`, migration.ID).Scan(&critical); err != nil {
		return GateSnapshot{}, fmt.Errorf("balancev2: gate mismatches: %w", err)
	}
	if err := r.db.QueryRowContext(ctx, `
		SELECT count(*) FROM data_migration_mismatches
		WHERE migration_id = $1 AND classification = $2
		  AND status IN ('open', 'classified', 'repair_pending', 'repairing', 'blocked')`, migration.ID, ClassificationBackfillMissing).Scan(&targetMissing); err != nil {
		return GateSnapshot{}, fmt.Errorf("balancev2: gate target missing rows: %w", err)
	}
	var comparisons, matches, mismatches, comparisonErrors int64
	if err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(sum(processed_count), 0), COALESCE(sum(match_count), 0),
		       COALESCE(sum(mismatch_count), 0), COALESCE(sum(error_count), 0)
		FROM data_migration_runs
		WHERE migration_id = $1 AND status = 'completed'
		  AND run_type IN ('sample', 'bucket', 'full', 'pre_cutover', 'post_cutover')`, migration.ID).Scan(&comparisons, &matches, &mismatches, &comparisonErrors); err != nil {
		return GateSnapshot{}, fmt.Errorf("balancev2: gate comparisons: %w", err)
	}
	var latestType string
	var latestAt sql.NullTime
	var evidence []byte
	latestErr := r.db.QueryRowContext(ctx, `
		SELECT run_type, finished_at, evidence
		FROM data_migration_runs
		WHERE migration_id = $1 AND status = 'completed'
		ORDER BY finished_at DESC NULLS LAST LIMIT 1`, migration.ID).Scan(&latestType, &latestAt, &evidence)
	if latestErr != nil && !errors.Is(latestErr, sql.ErrNoRows) {
		return GateSnapshot{}, fmt.Errorf("balancev2: gate latest reconciliation: %w", latestErr)
	}
	var preCutoverProcessed int64
	var preCutoverEvidence []byte
	preCutoverErr := r.db.QueryRowContext(ctx, `
		SELECT processed_count, evidence
		FROM data_migration_runs
		WHERE migration_id = $1 AND status = 'completed' AND run_type = 'pre_cutover'
		ORDER BY finished_at DESC NULLS LAST LIMIT 1`, migration.ID).Scan(&preCutoverProcessed, &preCutoverEvidence)
	if preCutoverErr != nil && !errors.Is(preCutoverErr, sql.ErrNoRows) {
		return GateSnapshot{}, fmt.Errorf("balancev2: gate pre-cutover evidence: %w", preCutoverErr)
	}
	backupFresh := false
	if len(preCutoverEvidence) > 0 {
		var latestEvidence map[string]any
		if json.Unmarshal(preCutoverEvidence, &latestEvidence) == nil {
			backupFresh, _ = latestEvidence["backup_fresh"].(bool)
		}
	}
	preCutoverComplete := preCutoverErr == nil && preCutoverProcessed >= sourceCount
	coverage := 1.0
	if sourceCount > 0 {
		coverage = float64(targetCount) / float64(sourceCount)
		if coverage > 1 {
			coverage = 1
		}
	}
	shadowRatio := 0.0
	if comparisons > 0 {
		shadowRatio = float64(matches) / float64(comparisons)
	}
	snapshot := GateSnapshot{
		Passed:                critical == 0 && coverage >= 1 && comparisons > 0 && comparisonErrors == 0 && shadowRatio >= 0.999 && latestType != "" && backupFresh && preCutoverComplete,
		FreshAt:               time.Now().UTC(),
		UnresolvedCritical:    critical,
		TargetMissingEligible: targetMissing,
		ShadowComparisons:     comparisons,
		ShadowMatches:         matches,
		ComparisonErrors:      comparisonErrors,
		ShadowSuccessRatio:    shadowRatio,
		FallbackRate:          0,
		TargetCoverageRatio:   coverage,
		LatestReconciliation:  latestType,
		BackupFresh:           backupFresh,
		PreCutoverComplete:    preCutoverComplete,
	}
	unresolvedMismatches.WithLabelValues(MigrationName, "critical").Set(float64(critical))
	if !snapshot.Passed {
		snapshot.Reason = fmt.Sprintf("critical=%d coverage=%.4f comparisons=%d matches=%d mismatches=%d errors=%d latest=%s backup_fresh=%t pre_cutover_complete=%t", critical, coverage, comparisons, matches, mismatches, comparisonErrors, latestType, backupFresh, preCutoverComplete)
	}
	return snapshot, nil
}

func (r *ControlRepository) GetMismatch(ctx context.Context, id uuid.UUID) (Mismatch, error) {
	return scanMismatchRow(r.db.QueryRowContext(ctx, `SELECT `+mismatchColumns+` FROM data_migration_mismatches WHERE id = $1`, id))
}

const mismatchColumns = `id, migration_id, resource_key_hash, resource_public_key,
	classification, status, severity, field_mask, source_version, target_version,
	source_checksum, target_checksum, occurrence_count, first_seen_at, last_seen_at,
	repair_attempt_count, last_error_code, created_at, updated_at`

func scanMismatchRow(row rowScanner) (Mismatch, error) {
	var item Mismatch
	var classification, lastError sql.NullString
	var sourceVersion, targetVersion sql.NullInt64
	if err := row.Scan(&item.ID, &item.MigrationID, &item.ResourceKeyHash,
		&item.ResourcePublicKey, &classification, &item.Status, &item.Severity,
		&item.FieldMask, &sourceVersion, &targetVersion, &item.SourceChecksum,
		&item.TargetChecksum, &item.OccurrenceCount, &item.FirstSeenAt,
		&item.LastSeenAt, &item.RepairAttemptCount, &lastError, &item.CreatedAt,
		&item.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Mismatch{}, ErrMigrationNotFound
		}
		return Mismatch{}, err
	}
	item.Classification = classification.String
	item.LastErrorCode = lastError.String
	if sourceVersion.Valid {
		value := sourceVersion.Int64
		item.SourceVersion = &value
	}
	if targetVersion.Valid {
		value := targetVersion.Int64
		item.TargetVersion = &value
	}
	return item, nil
}

func (r *ControlRepository) ListMismatches(ctx context.Context, migrationID uuid.UUID, status string, limit, offset int) ([]Mismatch, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+mismatchColumns+` FROM data_migration_mismatches
		WHERE migration_id = $1 AND ($2 = '' OR status = $2)
		ORDER BY last_seen_at DESC, id LIMIT $3 OFFSET $4`, migrationID, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("balancev2: list mismatches: %w", err)
	}
	defer rows.Close()
	items := make([]Mismatch, 0)
	for rows.Next() {
		item, scanErr := scanMismatchRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("balancev2: list mismatches rows: %w", err)
	}
	return items, nil
}

const repairColumns = `id, migration_id, mismatch_id, resource_key_hash, repair_type,
	expected_source_version, status, attempt_count, created_by, approved_by, reason,
	started_at, finished_at, error_code, created_at, updated_at`

func (r *ControlRepository) CreateRepair(ctx context.Context, mismatchID uuid.UUID, actor, reason string) (Repair, error) {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return Repair{}, fmt.Errorf("balancev2: repair actor and reason are required")
	}
	mismatch, err := r.GetMismatch(ctx, mismatchID)
	if err != nil {
		return Repair{}, err
	}
	if mismatch.Classification == ClassificationSourceCorruption || mismatch.Classification == ClassificationSharedProjectionBug {
		return Repair{}, fmt.Errorf("%w: source or shared projection evidence must be investigated before a target repair", ErrGateBlocked)
	}
	switch mismatch.Classification {
	case ClassificationBackfillMissing, ClassificationStaleBackfill, ClassificationLiveWriteGap, ClassificationTargetCorruption:
	default:
		return Repair{}, fmt.Errorf("%w: mismatch classification is not eligible for a target repair", ErrGateBlocked)
	}
	var expected any
	if mismatch.SourceVersion != nil {
		expected = *mismatch.SourceVersion
	}
	var repair Repair
	err = r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			INSERT INTO data_migration_repairs (
				id, migration_id, mismatch_id, resource_key_hash, repair_type,
				expected_source_version, status, created_by, reason, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, 'target_rebuild', $5, 'pending_approval', $6, $7, now(), now())
			ON CONFLICT DO NOTHING
			RETURNING `+repairColumns,
			uuid.New(), mismatch.MigrationID, mismatch.ID, mismatch.ResourceKeyHash,
			expected, actor, reason)
		created := true
		if scanErr := scanRepairRow(row, &repair); errors.Is(scanErr, sql.ErrNoRows) {
			created = false
			if lookupErr := scanRepairRow(tx.QueryRowContext(ctx, `
				SELECT `+repairColumns+` FROM data_migration_repairs
				WHERE migration_id = $1 AND mismatch_id = $2 AND repair_type = 'target_rebuild'
				  AND ((expected_source_version IS NULL AND $3::bigint IS NULL)
				       OR expected_source_version = $3)
				ORDER BY created_at DESC LIMIT 1`, mismatch.MigrationID, mismatch.ID, expected), &repair); lookupErr != nil {
				return fmt.Errorf("balancev2: load existing repair: %w", lookupErr)
			}
		} else if scanErr != nil {
			return fmt.Errorf("balancev2: create repair: %w", scanErr)
		}
		if !created {
			return nil
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE data_migration_mismatches
			SET status = 'repair_pending', updated_at = now()
			WHERE id = $1`, mismatch.ID)
		return err
	})
	if err != nil {
		return Repair{}, err
	}
	return repair, nil
}

func (r *ControlRepository) GetRepair(ctx context.Context, id uuid.UUID) (Repair, error) {
	var repair Repair
	if err := scanRepairRow(r.db.QueryRowContext(ctx, `SELECT `+repairColumns+` FROM data_migration_repairs WHERE id = $1`, id), &repair); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Repair{}, ErrMigrationNotFound
		}
		return Repair{}, err
	}
	return repair, nil
}

func scanRepairRow(row rowScanner, repair *Repair) error {
	var expected sql.NullInt64
	var approved, errorCode sql.NullString
	var startedAt, finishedAt sql.NullTime
	if err := row.Scan(&repair.ID, &repair.MigrationID, &repair.MismatchID,
		&repair.ResourceKeyHash, &repair.RepairType, &expected, &repair.Status,
		&repair.AttemptCount, &repair.CreatedBy, &approved, &repair.Reason,
		&startedAt, &finishedAt, &errorCode, &repair.CreatedAt, &repair.UpdatedAt); err != nil {
		return err
	}
	if expected.Valid {
		value := expected.Int64
		repair.ExpectedSourceVersion = &value
	}
	repair.ApprovedBy = approved.String
	repair.ErrorCode = errorCode.String
	if startedAt.Valid {
		value := startedAt.Time
		repair.StartedAt = &value
	}
	if finishedAt.Valid {
		value := finishedAt.Time
		repair.FinishedAt = &value
	}
	return nil
}

func (r *ControlRepository) ApproveRepair(ctx context.Context, id uuid.UUID, approver, reason string) (Repair, error) {
	if strings.TrimSpace(approver) == "" || strings.TrimSpace(reason) == "" {
		return Repair{}, fmt.Errorf("balancev2: repair approver and reason are required")
	}
	var repair Repair
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var current Repair
		if err := scanRepairRow(tx.QueryRowContext(ctx, `SELECT `+repairColumns+` FROM data_migration_repairs WHERE id = $1 FOR UPDATE`, id), &current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrMigrationNotFound
			}
			return err
		}
		if current.Status != "pending_approval" {
			return fmt.Errorf("balancev2: repair is not pending approval")
		}
		if current.CreatedBy == approver {
			return ErrApprovalRequired
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE data_migration_repairs
			SET status = 'approved', approved_by = $1, reason = reason || ' | approval: ' || $2,
				updated_at = now()
			WHERE id = $3`, approver, boundedErrorCode(reason), id); err != nil {
			return fmt.Errorf("balancev2: approve repair: %w", err)
		}
		if err := scanRepairRow(tx.QueryRowContext(ctx, `SELECT `+repairColumns+` FROM data_migration_repairs WHERE id = $1`, id), &repair); err != nil {
			return err
		}
		return nil
	})
	return repair, err
}

func (r *ControlRepository) MarkRepairRunning(ctx context.Context, id uuid.UUID, owner string) error {
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE data_migration_repairs
			SET status = 'running', attempt_count = attempt_count + 1,
				started_at = now(), updated_at = now(),
				lease_owner = $2, lease_expires_at = now() + $3::interval
			WHERE id = $1 AND status = 'approved'`, id, owner, intervalLiteral(repairLeaseDuration))
		if err != nil {
			return err
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr != nil {
			return affectedErr
		} else if affected != 1 {
			return fmt.Errorf("balancev2: repair is no longer approved")
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE data_migration_mismatches
			SET status = 'repairing', updated_at = now()
			WHERE id = (SELECT mismatch_id FROM data_migration_repairs WHERE id = $1)`, id)
		return err
	})
}

// ReclaimStuckRepairs resets any repair a crashed process left in 'running'
// past its lease back to 'pending_approval' (clearing the prior approval),
// letting the existing ApproveRepair maker/checker flow retry it rather than
// requiring a bespoke recovery path. Mirrors AcquireCheckpoint's lease
// reclaim, but repairs have no natural "next acquire" moment to piggyback
// on, so this runs opportunistically from the lifecycle worker instead.
func (r *ControlRepository) ReclaimStuckRepairs(ctx context.Context) (int64, error) {
	var reclaimed int64
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			UPDATE data_migration_repairs
			SET status = 'pending_approval', approved_by = NULL,
				lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
			WHERE status = 'running' AND lease_expires_at IS NOT NULL AND lease_expires_at < now()
			RETURNING mismatch_id`)
		if err != nil {
			return fmt.Errorf("balancev2: reclaim stuck repairs: %w", err)
		}
		defer rows.Close()
		var mismatchIDs []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if scanErr := rows.Scan(&id); scanErr != nil {
				return fmt.Errorf("balancev2: scan reclaimed repair: %w", scanErr)
			}
			mismatchIDs = append(mismatchIDs, id)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("balancev2: iterate reclaimed repairs: %w", err)
		}
		reclaimed = int64(len(mismatchIDs))
		for _, id := range mismatchIDs {
			if _, err := tx.ExecContext(ctx, `
				UPDATE data_migration_mismatches SET status = 'repair_pending', updated_at = now()
				WHERE id = $1 AND status = 'repairing'`, id); err != nil {
				return fmt.Errorf("balancev2: reset mismatch after reclaim: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return reclaimed, nil
}

func (r *ControlRepository) FinishRepair(ctx context.Context, repairID, mismatchID uuid.UUID, success bool, errorCode string) error {
	status := "completed"
	if !success {
		status = "failed"
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE data_migration_repairs
		SET status = $1, finished_at = now(), error_code = NULLIF($2, ''), updated_at = now(),
			lease_owner = NULL, lease_expires_at = NULL
		WHERE id = $3`, status, boundedErrorCode(errorCode), repairID)
	if err != nil {
		return err
	}
	if success {
		_, err = r.db.ExecContext(ctx, `
			UPDATE data_migration_mismatches
			SET status = 'repaired', repair_attempt_count = repair_attempt_count + 1,
				updated_at = now()
			WHERE id = $1`, mismatchID)
	} else {
		_, err = r.db.ExecContext(ctx, `
			UPDATE data_migration_mismatches
			SET status = 'blocked', repair_attempt_count = repair_attempt_count + 1,
				last_error_code = NULLIF($2, ''), updated_at = now()
			WHERE id = $1`, mismatchID, boundedErrorCode(errorCode))
	}
	return err
}
