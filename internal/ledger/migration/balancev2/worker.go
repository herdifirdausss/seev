package balancev2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/migrationkit"
)

type reconcileStats struct {
	processed int64
	matches   int64
	mismatches int64
	errors    int64
}

// BackfillOnce advances one bounded keyset page. It never locks the source
// table for the duration of the scan; each page is a short transaction and
// the source version predicate makes retries idempotent.
func (r *Runtime) BackfillOnce(ctx context.Context) error {
	migration, err := r.migration(ctx)
	if err != nil {
		return err
	}
	if migration.State != string(migrationkit.Backfilling) {
		return nil
	}
	checkpoint, acquired, err := r.controls.AcquireCheckpoint(ctx, migration.ID, "backfill", "account_id", r.owner, r.cfg.BackfillStatementTimeout)
	if err != nil || !acquired {
		return err
	}
	started := time.Now()
	var empty bool
	var processed, updated, failed int64
	err = r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if err := setLocalTimeouts(ctx, tx, r.cfg.BackfillStatementTimeout, r.cfg.BackfillLockTimeout); err != nil {
			return err
		}
		lastKey, parseErr := checkpointLastKey(checkpoint.LastSourceKey)
		if parseErr != nil {
			return parseErr
		}
		rows, err := readSourceBatch(ctx, tx, lastKey, r.cfg.BackfillBatchSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			empty = true
			checkpoint.Status = "completed"
			checkpoint.LeaseExpiresAt = timePtr(time.Now().UTC())
			return r.controls.SaveCheckpointTx(ctx, tx, checkpoint)
		}
		for _, source := range rows {
			processed++
			checkpoint.LastSourceKey = source.AccountID.String()
			if checkpoint.WatermarkVersion == nil || source.SourceVersion > *checkpoint.WatermarkVersion {
				value := source.SourceVersion
				checkpoint.WatermarkVersion = &value
			}
			target, transformErr := Transform(source, nil)
			if transformErr != nil {
				failed++
				if recordErr := r.controls.RecordComparison(ctx, migration.ID, Comparison{
					AccountID: source.AccountID, ResourceLayer: "source_target", Result: "unsupported",
					Classification: ClassificationUnsupportedLegacyRow, Severity: "critical",
					SourceVersion: source.SourceVersion, ErrorCode: "backfill_transform_error",
				}); recordErr != nil {
					return recordErr
				}
				continue
			}
			changed, upsertErr := upsertTarget(ctx, tx, target)
			if upsertErr != nil {
				failed++
				return upsertErr
			}
			if changed {
				updated++
			}
		}
		checkpoint.ProcessedCount += processed
		checkpoint.UpdatedCount += updated
		checkpoint.FailedCount += failed
		checkpoint.Status = "running"
		checkpoint.LeaseExpiresAt = timePtr(time.Now().UTC().Add(r.cfg.BackfillStatementTimeout))
		return r.controls.SaveCheckpointTx(ctx, tx, checkpoint)
	})
	if err != nil {
		backfillRowsTotal.WithLabelValues(MigrationName, "failed").Add(float64(failed))
		backfillDuration.WithLabelValues(MigrationName, "failed").Observe(time.Since(started).Seconds())
		return err
	}
	backfillRowsTotal.WithLabelValues(MigrationName, "processed").Add(float64(processed))
	backfillRowsTotal.WithLabelValues(MigrationName, "updated").Add(float64(updated))
	backfillDuration.WithLabelValues(MigrationName, "success").Observe(time.Since(started).Seconds())
	if sourceCount, countErr := countSource(ctx, r.db); countErr == nil {
		if targetCount, targetErr := countTarget(ctx, r.db); targetErr == nil && sourceCount > 0 {
			backfillProgress.WithLabelValues(MigrationName).Set(float64(targetCount) / float64(sourceCount))
		}
	}
	if empty {
		_, transitionErr := r.controls.Transition(ctx, TransitionRequest{
			MigrationID: migration.ID, ToState: string(migrationkit.DualWriteShadow),
			RequestedBy: "worker:balancev2", Reason: "bounded backfill reached end of source keyset",
			ExpectedVersion: migration.Version,
		}, GateSnapshot{Passed: true, FreshAt: time.Now().UTC(), Reason: "backfill checkpoint complete"})
		if transitionErr != nil && !errors.Is(transitionErr, ErrOptimisticConflict) {
			return transitionErr
		}
		r.Refresh()
	} else if r.cfg.BackfillSleep > 0 {
		timer := time.NewTimer(r.cfg.BackfillSleep)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func setLocalTimeouts(ctx context.Context, tx *sql.Tx, statement, lock time.Duration) error {
	if statement > 0 {
		if _, err := tx.ExecContext(ctx, `SELECT set_config('statement_timeout', $1, true)`, strconv.FormatInt(statement.Milliseconds(), 10)); err != nil {
			return fmt.Errorf("balancev2: set statement timeout: %w", err)
		}
	}
	if lock > 0 {
		if _, err := tx.ExecContext(ctx, `SELECT set_config('lock_timeout', $1, true)`, strconv.FormatInt(lock.Milliseconds(), 10)); err != nil {
			return fmt.Errorf("balancev2: set lock timeout: %w", err)
		}
	}
	return nil
}

func checkpointLastKey(value string) (*uuid.UUID, error) {
	if value == "" {
		return nil, nil
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("balancev2: invalid checkpoint source key: %w", err)
	}
	return &id, nil
}

func timePtr(value time.Time) *time.Time { return &value }

// ReconcileOnce performs one bounded source/target, target/ledger, and
// source/ledger comparison page. The worker intentionally records hashes,
// versions, and checksums only; it never persists an account identifier.
func (r *Runtime) ReconcileOnce(ctx context.Context) error {
	migration, err := r.migration(ctx)
	if err != nil {
		return err
	}
	if !targetWriteStage(migration) {
		return nil
	}
	checkpoint, acquired, err := r.controls.AcquireCheckpoint(ctx, migration.ID, "reconcile", "account_id", r.owner, r.cfg.WorkerInterval*2)
	if err != nil || !acquired {
		return err
	}
	run, err := r.controls.CreateRun(ctx, migration.ID, RunBucket, time.Now().UTC().Format(time.RFC3339Nano), "worker:balancev2")
	if err != nil {
		return err
	}
	done, stats, pageErr := r.reconcilePage(ctx, migration, &checkpoint, run.ID)
	if pageErr != nil {
		_ = r.controls.FinishRun(ctx, run.ID, "failed", stats.processed, stats.matches, stats.mismatches, stats.errors+1, map[string]any{"backup_fresh": false})
		return pageErr
	}
	if stats.errors > 0 {
		_ = r.controls.FinishRun(ctx, run.ID, "failed", stats.processed, stats.matches, stats.mismatches, stats.errors, map[string]any{"backup_fresh": false})
		return fmt.Errorf("balancev2: reconciliation page recorded %d comparison errors", stats.errors)
	}
	if done {
		r.Refresh()
	}
	return r.controls.FinishRun(ctx, run.ID, "completed", stats.processed, stats.matches, stats.mismatches, stats.errors, map[string]any{
		"backup_fresh": false, "checkpoint_complete": done,
	})
}

// RunPreCutoverReconciliation is the explicit operator-triggered full pass.
// The backup flag is evidence supplied by the operator after checking the
// deployment backup system; it cannot be inferred from application reads.
func (r *Runtime) RunPreCutoverReconciliation(ctx context.Context, actor string, backupFresh bool) error {
	migration, err := r.migration(ctx)
	if err != nil {
		return err
	}
	if !targetWriteStage(migration) {
		return fmt.Errorf("balancev2: pre-cutover reconciliation requires dual-write lifecycle state")
	}
	checkpoint, acquired, err := r.controls.AcquireCheckpoint(ctx, migration.ID, "reconcile_pre_cutover", "account_id", r.owner+"-pre", r.cfg.WorkerInterval*2)
	if err != nil {
		return err
	}
	if !acquired {
		return fmt.Errorf("balancev2: pre-cutover reconciliation is already running")
	}
	checkpoint.LastSourceKey = ""
	checkpoint.ProcessedCount = 0
	checkpoint.UpdatedCount = 0
	checkpoint.SkippedCount = 0
	checkpoint.FailedCount = 0
	checkpoint.Status = "running"
	if err := r.controls.SaveCheckpoint(ctx, checkpoint); err != nil {
		return err
	}
	run, err := r.controls.CreateRun(ctx, migration.ID, RunPreCutover, time.Now().UTC().Format(time.RFC3339Nano), actor)
	if err != nil {
		return err
	}
	var totals reconcileStats
	for {
		done, stats, pageErr := r.reconcilePage(ctx, migration, &checkpoint, run.ID)
		totals.processed += stats.processed
		totals.matches += stats.matches
		totals.mismatches += stats.mismatches
		totals.errors += stats.errors
		if pageErr != nil {
			_ = r.controls.FinishRun(ctx, run.ID, "failed", totals.processed, totals.matches, totals.mismatches, totals.errors+1, map[string]any{"backup_fresh": backupFresh})
			return pageErr
		}
		if stats.errors > 0 {
			_ = r.controls.FinishRun(ctx, run.ID, "failed", totals.processed, totals.matches, totals.mismatches, totals.errors, map[string]any{"backup_fresh": backupFresh})
			return fmt.Errorf("balancev2: pre-cutover reconciliation page recorded %d comparison errors", stats.errors)
		}
		if done {
			break
		}
		if err := ctx.Err(); err != nil {
			_ = r.controls.FinishRun(context.Background(), run.ID, "failed", totals.processed, totals.matches, totals.mismatches, totals.errors+1, map[string]any{"backup_fresh": backupFresh})
			return err
		}
	}
	r.Refresh()
	return r.controls.FinishRun(ctx, run.ID, "completed", totals.processed, totals.matches, totals.mismatches, totals.errors, map[string]any{
		"backup_fresh": backupFresh, "checkpoint_complete": true,
	})
}

func (r *Runtime) reconcilePage(ctx context.Context, migration Migration, checkpoint *Checkpoint, runID uuid.UUID) (bool, reconcileStats, error) {
	var stats reconcileStats
	lastKey, err := checkpointLastKey(checkpoint.LastSourceKey)
	if err != nil {
		return false, stats, err
	}
	rows, err := readSourceBatch(ctx, r.db, lastKey, r.cfg.ReconcileBatchSize)
	if err != nil {
		return false, stats, err
	}
	if len(rows) == 0 {
		checkpoint.Status = "completed"
		checkpoint.LastSourceKey = ""
		checkpoint.LeaseExpiresAt = timePtr(time.Now().UTC())
		if err := r.controls.SaveCheckpoint(ctx, *checkpoint); err != nil {
			return false, stats, err
		}
		return true, stats, nil
	}
	for _, source := range rows {
		target, targetErr := targetForAccount(ctx, r.db, source.AccountID)
		comparison := CompareRows(source, target)
		if targetErr != nil {
			comparison = Comparison{AccountID: source.AccountID, ResourceLayer: "source_target", Result: "target_error", Classification: ClassificationTargetCorruption, Severity: "critical", SourceVersion: source.SourceVersion, ErrorCode: "target_read_error"}
		}
		if recordErr := r.controls.RecordComparison(ctx, migration.ID, comparison); recordErr != nil {
			stats.errors++
		} else if comparison.Result == "match" {
			stats.matches++
		} else {
			stats.mismatches++
		}
		ledgerTotal, ledgerErr := r.sourceLedgerTotal(ctx, source.AccountID)
		ledgerComparison := Comparison{AccountID: source.AccountID, ResourceLayer: "source_ledger", Result: "match", Classification: "match", Severity: "none", SourceVersion: source.SourceVersion}
		if ledgerErr != nil {
			ledgerComparison.Result = "ledger_error"
			ledgerComparison.Classification = ClassificationSourceCorruption
			ledgerComparison.Severity = "critical"
			ledgerComparison.ErrorCode = "ledger_total_error"
		} else if ledgerTotal != source.Balance {
			ledgerComparison.Result = "ledger_mismatch"
			ledgerComparison.Classification = ClassificationSourceCorruption
			ledgerComparison.Severity = "critical"
			ledgerComparison.FieldMask = FieldAvailable
			ledgerComparison.ErrorCode = "source_ledger_balance_mismatch"
		}
		if recordErr := r.controls.RecordComparison(ctx, migration.ID, ledgerComparison); recordErr != nil {
			stats.errors++
		}
		targetLedger := comparison
		targetLedger.ResourceLayer = "target_ledger"
		if ledgerComparison.Result != "match" {
			targetLedger.Classification = ClassificationSourceCorruption
			targetLedger.Severity = "critical"
			targetLedger.Result = "source_ledger_blocked"
			targetLedger.ErrorCode = ledgerComparison.ErrorCode
		} else if targetLedger.Result != "match" {
			targetLedger.Classification = ClassificationSharedProjectionBug
		}
		if recordErr := r.controls.RecordComparison(ctx, migration.ID, targetLedger); recordErr != nil {
			stats.errors++
		}
		checkpoint.LastSourceKey = source.AccountID.String()
		checkpoint.ProcessedCount++
		if checkpoint.WatermarkVersion == nil || source.SourceVersion > *checkpoint.WatermarkVersion {
			value := source.SourceVersion
			checkpoint.WatermarkVersion = &value
		}
	}
	checkpoint.Status = "running"
	checkpoint.LeaseExpiresAt = timePtr(time.Now().UTC().Add(r.cfg.WorkerInterval * 2))
	if err := r.controls.SaveCheckpoint(ctx, *checkpoint); err != nil {
		return false, stats, err
	}
	return false, stats, nil
}

func (r *Runtime) sourceLedgerTotal(ctx context.Context, accountID uuid.UUID) (int64, error) {
	var total int64
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN direction = 'credit' THEN amount ELSE -amount END), 0)
		FROM ledger_entries WHERE account_id = $1`, accountID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("balancev2: source ledger total: %w", err)
	}
	return total, nil
}
