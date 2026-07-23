package repository

//go:generate mockgen -source=scheduled_transaction_repository.go -destination=scheduled_transaction_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

// ScheduledTransactionRepository persists recurring/deferred user
// transactions (docs/roadmap/archive/19 Task T1). Write methods that mutate execution
// state (MarkSuccess/MarkBusinessFailure) take a *sql.Tx — the caller
// (internal/ledger/service/schedule) owns transaction boundaries, same
// pattern as every other repository in this module.
type ScheduledTransactionRepository interface {
	Create(ctx context.Context, tx *sql.Tx, id, userID uuid.UUID, cmdPayload []byte, kind string, runAtDate time.Time, dayOfMonth *int, createdBy string) error

	// GetByID is a read-only lookup outside any transaction.
	GetByID(ctx context.Context, id uuid.UUID) (model.ScheduledTransaction, error)

	// List returns a user's own scheduled transactions, newest first.
	List(ctx context.Context, userID uuid.UUID) ([]model.ScheduledTransaction, error)

	// ListDue returns every 'active' row due to run on asOf — 'once'/'daily'
	// match on run_at_date<=asOf and not yet run today; 'monthly'
	// additionally requires day_of_month to equal asOf's day of month.
	// Read-only, outside any transaction (mirrors PendingAdjustmentRepository's
	// GetByID/List pattern).
	ListDue(ctx context.Context, asOf time.Time) ([]model.ScheduledTransaction, error)

	// MarkSuccess records a successful run: last_run_date=asOf, last_error
	// cleared, and status->'finished' when finish is true ('once' schedules
	// after their single run).
	MarkSuccess(ctx context.Context, tx *sql.Tx, id uuid.UUID, asOf time.Time, finish bool) error

	// MarkBusinessFailure records last_error WITHOUT touching last_run_date
	// (docs/roadmap/archive/19 Task T1 step 3) — the row stays due for the next
	// evaluation. status->'failed' when terminal is true ('once' schedules;
	// a recurring schedule stays 'active' and retries at its next
	// occurrence).
	MarkBusinessFailure(ctx context.Context, tx *sql.Tx, id uuid.UUID, errMsg string, terminal bool) error

	// Pause/Resume/Cancel are atomic conditional UPDATEs (WHERE status=<from>)
	// — same K3 pattern as PendingAdjustmentRepository.MarkApproved. Returns
	// rows affected: 0 means the row wasn't in the expected starting status
	// (already paused/cancelled/finished, or a concurrent request won the race).
	Pause(ctx context.Context, tx *sql.Tx, id uuid.UUID) (int64, error)
	Resume(ctx context.Context, tx *sql.Tx, id uuid.UUID) (int64, error)
	Cancel(ctx context.Context, tx *sql.Tx, id uuid.UUID) (int64, error)
}

type scheduledTransactionRepo struct {
	db database.DatabaseSQL
}

func NewScheduledTransactionRepository(db database.DatabaseSQL) ScheduledTransactionRepository {
	return &scheduledTransactionRepo{db: db}
}

func (r *scheduledTransactionRepo) Create(ctx context.Context, tx *sql.Tx, id, userID uuid.UUID, cmdPayload []byte, kind string, runAtDate time.Time, dayOfMonth *int, createdBy string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO scheduled_transactions (id, user_id, cmd_payload, schedule_kind, run_at_date, day_of_month, status, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'active', $7, now())`,
		id, userID, cmdPayload, kind, runAtDate, dayOfMonth, createdBy,
	)
	if err != nil {
		return fmt.Errorf("create scheduled transaction: %w", err)
	}
	return nil
}

func scanScheduledTransaction(scan func(dest ...any) error) (model.ScheduledTransaction, error) {
	var (
		st          model.ScheduledTransaction
		dayOfMonth  sql.NullInt32
		lastRunDate sql.NullTime
		lastError   sql.NullString
	)
	err := scan(&st.ID, &st.UserID, &st.CmdPayload, &st.ScheduleKind, &st.RunAtDate, &dayOfMonth,
		&st.Status, &lastRunDate, &lastError, &st.CreatedBy, &st.CreatedAt, &st.UpdatedAt)
	if err != nil {
		return model.ScheduledTransaction{}, err
	}
	if dayOfMonth.Valid {
		v := int(dayOfMonth.Int32)
		st.DayOfMonth = &v
	}
	if lastRunDate.Valid {
		st.LastRunDate = &lastRunDate.Time
	}
	if lastError.Valid {
		st.LastError = &lastError.String
	}
	return st, nil
}

const scheduledTransactionColumns = `id, user_id, cmd_payload, schedule_kind, run_at_date, day_of_month,
	       status, last_run_date, last_error, created_by, created_at, updated_at`

func (r *scheduledTransactionRepo) GetByID(ctx context.Context, id uuid.UUID) (model.ScheduledTransaction, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+scheduledTransactionColumns+`
		FROM scheduled_transactions WHERE id = $1`, id)
	st, err := scanScheduledTransaction(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ScheduledTransaction{}, fmt.Errorf("%w: %s", apperror.ErrScheduledTransactionNotFound, id)
	}
	if err != nil {
		return model.ScheduledTransaction{}, fmt.Errorf("get scheduled transaction: %w", err)
	}
	return st, nil
}

func (r *scheduledTransactionRepo) List(ctx context.Context, userID uuid.UUID) ([]model.ScheduledTransaction, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+scheduledTransactionColumns+`
		FROM scheduled_transactions WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list scheduled transactions: %w", err)
	}
	defer rows.Close()
	return scanScheduledTransactionRows(rows)
}

func (r *scheduledTransactionRepo) ListDue(ctx context.Context, asOf time.Time) ([]model.ScheduledTransaction, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+scheduledTransactionColumns+`
		FROM scheduled_transactions
		WHERE status = 'active'
		  AND run_at_date <= $1
		  AND (last_run_date IS NULL OR last_run_date < $1)
		  AND (
		        schedule_kind IN ('once', 'daily')
		        OR (schedule_kind = 'monthly' AND day_of_month = EXTRACT(DAY FROM $1::date)::smallint)
		      )
		ORDER BY id`, asOf)
	if err != nil {
		return nil, fmt.Errorf("list due scheduled transactions: %w", err)
	}
	defer rows.Close()
	return scanScheduledTransactionRows(rows)
}

func scanScheduledTransactionRows(rows *sql.Rows) ([]model.ScheduledTransaction, error) {
	var out []model.ScheduledTransaction
	for rows.Next() {
		st, err := scanScheduledTransaction(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan scheduled transaction: %w", err)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduled transactions: %w", err)
	}
	return out, nil
}

func (r *scheduledTransactionRepo) MarkSuccess(ctx context.Context, tx *sql.Tx, id uuid.UUID, asOf time.Time, finish bool) error {
	status := "active"
	if finish {
		status = "finished"
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE scheduled_transactions
		SET last_run_date = $1, last_error = NULL, status = $2
		WHERE id = $3`,
		asOf, status, id,
	)
	if err != nil {
		return fmt.Errorf("mark scheduled transaction success: %w", err)
	}
	return nil
}

func (r *scheduledTransactionRepo) MarkBusinessFailure(ctx context.Context, tx *sql.Tx, id uuid.UUID, errMsg string, terminal bool) error {
	if terminal {
		_, err := tx.ExecContext(ctx, `
			UPDATE scheduled_transactions SET last_error = $1, status = 'failed' WHERE id = $2`,
			errMsg, id,
		)
		if err != nil {
			return fmt.Errorf("mark scheduled transaction failed: %w", err)
		}
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE scheduled_transactions SET last_error = $1 WHERE id = $2`,
		errMsg, id,
	)
	if err != nil {
		return fmt.Errorf("mark scheduled transaction business failure: %w", err)
	}
	return nil
}

func (r *scheduledTransactionRepo) Pause(ctx context.Context, tx *sql.Tx, id uuid.UUID) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE scheduled_transactions SET status = 'paused' WHERE id = $1 AND status = 'active'`, id)
	if err != nil {
		return 0, fmt.Errorf("pause scheduled transaction: %w", err)
	}
	return res.RowsAffected()
}

func (r *scheduledTransactionRepo) Resume(ctx context.Context, tx *sql.Tx, id uuid.UUID) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE scheduled_transactions SET status = 'active' WHERE id = $1 AND status = 'paused'`, id)
	if err != nil {
		return 0, fmt.Errorf("resume scheduled transaction: %w", err)
	}
	return res.RowsAffected()
}

func (r *scheduledTransactionRepo) Cancel(ctx context.Context, tx *sql.Tx, id uuid.UUID) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE scheduled_transactions SET status = 'finished' WHERE id = $1 AND status IN ('active', 'paused')`, id)
	if err != nil {
		return 0, fmt.Errorf("cancel scheduled transaction: %w", err)
	}
	return res.RowsAffected()
}
