package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// ScheduledOccurrenceRepository is the Ledger-owned persistence boundary for
// C5's durable scheduler.  A schedule row is configuration; an occurrence is
// the immutable execution identity that can be leased, retried, audited, and
// linked to one Ledger transaction.
type ScheduledOccurrenceRepository interface {
	CreateOrGet(context.Context, model.ScheduledOccurrence) (model.ScheduledOccurrence, error)
	Get(context.Context, uuid.UUID) (model.ScheduledOccurrence, error)
	List(context.Context, uuid.UUID, uuid.UUID, int, int) ([]model.ScheduledOccurrence, error)
	ListAttempts(context.Context, uuid.UUID) ([]model.ScheduledExecutionAttempt, error)
	Claim(context.Context, uuid.UUID, string, time.Time) (model.ScheduledOccurrence, error)
	RecordAttempt(context.Context, model.ScheduledExecutionAttempt) error
	FinishAttempt(context.Context, uuid.UUID, time.Time, string, bool, string, *uuid.UUID) error
	SetStatus(context.Context, uuid.UUID, string, string, *time.Time, *uuid.UUID) error
	RecordScheduleSuccess(context.Context, uuid.UUID) error
	RecordScheduleBusinessFailure(context.Context, uuid.UUID, string, int) (bool, error)
	SetScheduleLastRun(context.Context, uuid.UUID, time.Time, bool) error
	SetScheduleLastPlanned(context.Context, uuid.UUID, time.Time) error
	SetFee(context.Context, uuid.UUID, int64, *uuid.UUID) error
	BlockSchedule(context.Context, uuid.UUID, string) error
}

type scheduledOccurrenceRepo struct{ db database.DatabaseSQL }

func NewScheduledOccurrenceRepository(db database.DatabaseSQL) ScheduledOccurrenceRepository {
	return &scheduledOccurrenceRepo{db: db}
}

const scheduledOccurrenceColumns = `id, public_id, schedule_id, schedule_version,
scheduled_for, scheduled_local_date, status, idempotency_key, policy_snapshot,
fee_amount, fee_quote_id, ledger_transaction_id, attempt_count, next_attempt_at,
lease_owner, lease_expires_at, error_code, created_at, updated_at`

func scanScheduledOccurrence(scan func(dest ...any) error) (model.ScheduledOccurrence, error) {
	var (
		item          model.ScheduledOccurrence
		feeAmount     sql.NullInt64
		feeQuoteID    sql.NullString
		ledgerTxID    sql.NullString
		nextAttempt   sql.NullTime
		leaseOwner    sql.NullString
		leaseExpires  sql.NullTime
		errorCode     sql.NullString
		policyPayload []byte
	)
	err := scan(&item.ID, &item.PublicID, &item.ScheduleID, &item.ScheduleVersion,
		&item.ScheduledFor, &item.ScheduledLocalDate, &item.Status,
		&item.IdempotencyKey, &policyPayload, &feeAmount, &feeQuoteID,
		&ledgerTxID, &item.AttemptCount, &nextAttempt, &leaseOwner,
		&leaseExpires, &errorCode, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return model.ScheduledOccurrence{}, err
	}
	item.PolicySnapshot = json.RawMessage(policyPayload)
	if feeAmount.Valid {
		item.FeeAmount = &feeAmount.Int64
	}
	if feeQuoteID.Valid {
		id, parseErr := uuid.Parse(feeQuoteID.String)
		if parseErr != nil {
			return model.ScheduledOccurrence{}, fmt.Errorf("parse occurrence fee quote id: %w", parseErr)
		}
		item.FeeQuoteID = &id
	}
	if ledgerTxID.Valid {
		id, parseErr := uuid.Parse(ledgerTxID.String)
		if parseErr != nil {
			return model.ScheduledOccurrence{}, fmt.Errorf("parse occurrence ledger transaction id: %w", parseErr)
		}
		item.LedgerTransactionID = &id
	}
	if nextAttempt.Valid {
		item.NextAttemptAt = &nextAttempt.Time
	}
	if leaseOwner.Valid {
		item.LeaseOwner = &leaseOwner.String
	}
	if leaseExpires.Valid {
		item.LeaseExpiresAt = &leaseExpires.Time
	}
	if errorCode.Valid {
		item.ErrorCode = &errorCode.String
	}
	return item, nil
}

func (r *scheduledOccurrenceRepo) CreateOrGet(ctx context.Context, item model.ScheduledOccurrence) (model.ScheduledOccurrence, error) {
	if item.ID == uuid.Nil {
		item.ID = generalutil.NewV7()
	}
	if item.PublicID == "" {
		item.PublicID = item.ID.String()
	}
	if len(item.PolicySnapshot) == 0 {
		item.PolicySnapshot = json.RawMessage(`{}`)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO scheduled_occurrences
			(id,public_id,schedule_id,schedule_version,scheduled_for,scheduled_local_date,
			 status,idempotency_key,policy_snapshot,fee_amount,fee_quote_id)
		VALUES ($1,$2,$3,$4,$5,$6,'planned',$7,$8,$9,$10)
		ON CONFLICT (schedule_id, scheduled_for) DO NOTHING`, item.ID, item.PublicID,
		item.ScheduleID, item.ScheduleVersion, item.ScheduledFor,
		item.ScheduledLocalDate.Format("2006-01-02"), item.IdempotencyKey,
		item.PolicySnapshot, item.FeeAmount, item.FeeQuoteID)
	if err != nil {
		return model.ScheduledOccurrence{}, fmt.Errorf("create scheduled occurrence: %w", err)
	}
	stored, scanErr := scanScheduledOccurrence(r.db.QueryRowContext(ctx,
		`SELECT `+scheduledOccurrenceColumns+` FROM scheduled_occurrences
		 WHERE schedule_id=$1 AND scheduled_for=$2`, item.ScheduleID, item.ScheduledFor).Scan)
	if scanErr != nil {
		return model.ScheduledOccurrence{}, fmt.Errorf("read scheduled occurrence after create: %w", scanErr)
	}
	return stored, nil
}

func (r *scheduledOccurrenceRepo) Get(ctx context.Context, id uuid.UUID) (model.ScheduledOccurrence, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+scheduledOccurrenceColumns+`
		FROM scheduled_occurrences WHERE id=$1`, id)
	item, err := scanScheduledOccurrence(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ScheduledOccurrence{}, fmt.Errorf("scheduled occurrence %s: %w", id, sql.ErrNoRows)
	}
	if err != nil {
		return model.ScheduledOccurrence{}, fmt.Errorf("get scheduled occurrence: %w", err)
	}
	return item, nil
}

func (r *scheduledOccurrenceRepo) List(ctx context.Context, scheduleID, userID uuid.UUID, limit, offset int) ([]model.ScheduledOccurrence, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT ` + occurrenceColumnList("o") + `
		FROM scheduled_occurrences o
		JOIN scheduled_transactions s ON s.id=o.schedule_id
		WHERE o.schedule_id=$1`
	args := []any{scheduleID}
	if userID != uuid.Nil {
		query += ` AND s.user_id=$2`
		args = append(args, userID)
	}
	query += fmt.Sprintf(" ORDER BY o.scheduled_for DESC, o.id DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, limit, offset)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list scheduled occurrences: %w", err)
	}
	defer rows.Close()
	result := make([]model.ScheduledOccurrence, 0)
	for rows.Next() {
		item, scanErr := scanScheduledOccurrence(rows.Scan)
		if scanErr != nil {
			return nil, fmt.Errorf("scan scheduled occurrence: %w", scanErr)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduled occurrences: %w", err)
	}
	return result, nil
}

const scheduledAttemptColumns = `id, occurrence_id, attempt_number, phase, result,
retryable, error_code, ledger_transaction_id, started_at, finished_at, created_at`

func scanScheduledAttempt(scan func(dest ...any) error) (model.ScheduledExecutionAttempt, error) {
	var (
		item model.ScheduledExecutionAttempt
		errorCode, transactionID sql.NullString
		finishedAt sql.NullTime
	)
	if err := scan(&item.ID, &item.OccurrenceID, &item.AttemptNumber, &item.Phase,
		&item.Result, &item.Retryable, &errorCode, &transactionID, &item.StartedAt,
		&finishedAt, &item.CreatedAt); err != nil {
		return model.ScheduledExecutionAttempt{}, err
	}
	if errorCode.Valid { item.ErrorCode = &errorCode.String }
	if transactionID.Valid { id, err := uuid.Parse(transactionID.String); if err != nil { return model.ScheduledExecutionAttempt{}, err }; item.LedgerTransactionID = &id }
	if finishedAt.Valid { item.FinishedAt = &finishedAt.Time }
	return item, nil
}

func (r *scheduledOccurrenceRepo) ListAttempts(ctx context.Context, occurrenceID uuid.UUID) ([]model.ScheduledExecutionAttempt, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+scheduledAttemptColumns+`
		FROM scheduled_execution_attempts WHERE occurrence_id=$1 ORDER BY attempt_number`, occurrenceID)
	if err != nil { return nil, fmt.Errorf("list scheduled execution attempts: %w", err) }
	defer rows.Close()
	result := make([]model.ScheduledExecutionAttempt, 0)
	for rows.Next() {
		item, scanErr := scanScheduledAttempt(rows.Scan)
		if scanErr != nil { return nil, fmt.Errorf("scan scheduled execution attempt: %w", scanErr) }
		result = append(result, item)
	}
	if err := rows.Err(); err != nil { return nil, fmt.Errorf("iterate scheduled execution attempts: %w", err) }
	return result, nil
}

func (r *scheduledOccurrenceRepo) Claim(ctx context.Context, id uuid.UUID, owner string, now time.Time) (model.ScheduledOccurrence, error) {
	item, err := scanScheduledOccurrence(r.db.QueryRowContext(ctx, `UPDATE scheduled_occurrences
		SET status='processing', attempt_count=attempt_count+1,
			lease_owner=$2, lease_expires_at=$3 + interval '2 minutes', updated_at=now()
		WHERE id=$1 AND (
			(status IN ('planned','due','ready','retry_wait')
			 AND (next_attempt_at IS NULL OR next_attempt_at <= $3))
			OR (status='processing' AND lease_expires_at IS NOT NULL AND lease_expires_at < $3)
		  )
		RETURNING `+scheduledOccurrenceColumns, id, owner, now).Scan)
	if err != nil {
		return model.ScheduledOccurrence{}, err
	}
	// A crashed worker may leave its attempt open while the occurrence lease
	// expires. Close that historical attempt before the replacement attempt is
	// recorded so the execution history remains one row per attempt.
	if item.AttemptCount > 1 {
		if _, updateErr := r.db.ExecContext(ctx, `UPDATE scheduled_execution_attempts
			SET finished_at=$2,result='lease_expired',retryable=true,error_code='LEASE_EXPIRED'
			WHERE occurrence_id=$1 AND attempt_number < $3 AND finished_at IS NULL`,
			item.ID, now, item.AttemptCount); updateErr != nil {
			return model.ScheduledOccurrence{}, fmt.Errorf("close expired scheduled attempt: %w", updateErr)
		}
	}
	return item, nil
}

func (r *scheduledOccurrenceRepo) RecordAttempt(ctx context.Context, attempt model.ScheduledExecutionAttempt) error {
	if attempt.ID == uuid.Nil {
		attempt.ID = generalutil.NewV7()
	}
	if attempt.StartedAt.IsZero() {
		attempt.StartedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO scheduled_execution_attempts
		(id,occurrence_id,attempt_number,phase,result,retryable,error_code,
		 ledger_transaction_id,started_at,finished_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (occurrence_id,attempt_number) DO UPDATE SET phase=EXCLUDED.phase,
		 result=EXCLUDED.result,retryable=EXCLUDED.retryable,error_code=EXCLUDED.error_code,
		 ledger_transaction_id=EXCLUDED.ledger_transaction_id,finished_at=EXCLUDED.finished_at`,
		attempt.ID, attempt.OccurrenceID, attempt.AttemptNumber, attempt.Phase,
		attempt.Result, attempt.Retryable, attempt.ErrorCode, attempt.LedgerTransactionID,
		attempt.StartedAt, attempt.FinishedAt)
	if err != nil {
		return fmt.Errorf("record scheduled execution attempt: %w", err)
	}
	return nil
}

func (r *scheduledOccurrenceRepo) FinishAttempt(ctx context.Context, id uuid.UUID, finishedAt time.Time, result string, retryable bool, errorCode string, transactionID *uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_execution_attempts
		SET finished_at=$2,result=$3,retryable=$4,error_code=NULLIF($5,''),ledger_transaction_id=$6
		WHERE occurrence_id=$1 AND finished_at IS NULL`, id, finishedAt, result, retryable, errorCode, transactionID)
	if err != nil {
		return fmt.Errorf("finish scheduled execution attempt: %w", err)
	}
	return nil
}

func (r *scheduledOccurrenceRepo) SetStatus(ctx context.Context, id uuid.UUID, status, errorCode string, nextAttemptAt *time.Time, transactionID *uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_occurrences SET
		status=$2,error_code=NULLIF($3,''),next_attempt_at=$4,
		ledger_transaction_id=COALESCE($5,ledger_transaction_id),lease_owner=NULL,
		lease_expires_at=NULL,updated_at=now() WHERE id=$1`, id, status, errorCode,
		nextAttemptAt, transactionID)
	if err != nil {
		return fmt.Errorf("set scheduled occurrence status: %w", err)
	}
	return nil
}

func (r *scheduledOccurrenceRepo) RecordScheduleSuccess(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_transactions SET
		consecutive_failure_count=CASE WHEN status='active' THEN 0 ELSE consecutive_failure_count END,
		last_error=CASE WHEN status='active' THEN NULL ELSE last_error END,
		paused_reason=CASE WHEN status='active' THEN NULL ELSE paused_reason END,
		version=version+1 WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("record scheduled success: %w", err)
	}
	return nil
}

func (r *scheduledOccurrenceRepo) RecordScheduleBusinessFailure(ctx context.Context, id uuid.UUID, errorCode string, threshold int) (bool, error) {
	var status string
	err := r.db.QueryRowContext(ctx, `UPDATE scheduled_transactions SET
		consecutive_failure_count=consecutive_failure_count+1,
		last_error=$2,
		status=CASE WHEN consecutive_failure_count+1 >= $3 THEN 'blocked' ELSE status END,
		paused_reason=CASE WHEN consecutive_failure_count+1 >= $3 THEN 'consecutive_business_failures' ELSE paused_reason END,
		version=version+1 WHERE id=$1 AND status='active' RETURNING status`, id, errorCode, threshold).Scan(&status)
	if err != nil {
		return false, fmt.Errorf("record scheduled business failure: %w", err)
	}
	return status == model.ScheduleOccurrenceBlocked, nil
}

func (r *scheduledOccurrenceRepo) SetScheduleLastRun(ctx context.Context, id uuid.UUID, asOf time.Time, finish bool) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_transactions SET
		last_run_date=$2,
		last_error=CASE WHEN status='active' THEN NULL ELSE last_error END,
		status=CASE
			WHEN status='active' AND $3 THEN 'finished'
			WHEN status='active' THEN 'active'
			ELSE status
		END,
		version=version+1 WHERE id=$1`, id, asOf.UTC(), finish)
	if err != nil {
		return fmt.Errorf("set scheduled last run: %w", err)
	}
	return nil
}

// Retry atomically re-queues an occurrence only while it is still in a
// retryable terminal state. This prevents a stale retry request from
// replaying an occurrence that another worker has already completed.
func (r *scheduledOccurrenceRepo) Retry(ctx context.Context, occurrenceID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE scheduled_occurrences
		SET status='ready',
			error_code=NULL,
			next_attempt_at=NULL,
			lease_owner=NULL,
			lease_expires_at=NULL,
			updated_at=now()
		WHERE id=$1
		  AND status IN ('failed_business', 'blocked')
		  AND schedule_id IN (
				SELECT id
				FROM scheduled_transactions
				WHERE status IN ('active', 'paused')
		  )`, occurrenceID)
	if err != nil {
		return err
	}

	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *scheduledOccurrenceRepo) SetScheduleLastPlanned(ctx context.Context, id uuid.UUID, plannedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_transactions SET
		last_planned_at=$2,updated_at=now() WHERE id=$1 AND status='active'`, id, plannedAt.UTC())
	if err != nil {
		return fmt.Errorf("set scheduled last planned: %w", err)
	}
	return nil
}

func (r *scheduledOccurrenceRepo) SetFee(ctx context.Context, id uuid.UUID, fee int64, quoteID *uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_occurrences SET fee_amount=$2,fee_quote_id=$3,updated_at=now() WHERE id=$1`, id, fee, quoteID)
	if err != nil {
		return fmt.Errorf("set scheduled occurrence fee: %w", err)
	}
	return nil
}

func (r *scheduledOccurrenceRepo) BlockSchedule(ctx context.Context, id uuid.UUID, reason string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_transactions SET
		status='blocked',paused_reason=$2,version=version+1 WHERE id=$1 AND
		(status='active' OR (status='blocked' AND $2 IN ('fee_cap_exceeded','fee_consent_required')))`, id, reason)
	if err != nil {
		return fmt.Errorf("block scheduled transaction: %w", err)
	}
	return nil
}

// ConfirmFeeCap records the user's fee-cap confirmation and requeues blocked
// fee-cap occurrences so the durable dispatcher can execute them again.
func (r *scheduledOccurrenceRepo) ConfirmFeeCap(ctx context.Context, scheduleID, userID uuid.UUID, maxFeeAmount int64) error {
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE scheduled_transactions
			SET max_fee_amount = $3,
				status = 'active',
				paused_reason = NULL,
				consecutive_failure_count = 0,
				last_error = NULL,
				version = version + 1,
				updated_at = now()
			WHERE id = $1
			  AND user_id = $2
			  AND status IN ('paused', 'blocked')
			  AND paused_reason IN ('fee_cap_exceeded', 'fee_consent_required')
		`, scheduleID, userID, maxFeeAmount)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return sql.ErrNoRows
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE scheduled_occurrences
			SET status = 'ready',
				error_code = NULL,
				next_attempt_at = NULL,
				lease_owner = NULL,
				lease_expires_at = NULL,
				updated_at = now()
			WHERE schedule_id = $1
			  AND status = 'blocked'
			  AND error_code IN ('SCHEDULE_FEE_CAP_EXCEEDED', 'SCHEDULE_FEE_CONSENT_REQUIRED',
			                     'FEE_EXCEEDS_CONSENT', 'FEE_CONSENT_REQUIRED')
		`, scheduleID)
		return err
	})
}

func occurrenceColumnList(alias string) string {
	return alias + ".id," + alias + ".public_id," + alias + ".schedule_id," + alias + ".schedule_version," +
		alias + ".scheduled_for," + alias + ".scheduled_local_date," + alias + ".status," +
		alias + ".idempotency_key," + alias + ".policy_snapshot," + alias + ".fee_amount," +
		alias + ".fee_quote_id," + alias + ".ledger_transaction_id," + alias + ".attempt_count," +
		alias + ".next_attempt_at," + alias + ".lease_owner," + alias + ".lease_expires_at," +
		alias + ".error_code," + alias + ".created_at," + alias + ".updated_at"
}
