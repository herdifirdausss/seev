package repository

//go:generate mockgen -source=pending_adjustment_repository.go -destination=pending_adjustment_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

// PendingAdjustmentRepository persists the maker-checker workflow for manual
// balance adjustments (docs/plan/16 Task T1, decision K8). Write methods
// take a *sql.Tx — the caller (internal/ledger/service/adjustments) owns
// transaction boundaries, same pattern as every other repository in this
// module.
type PendingAdjustmentRepository interface {
	Create(ctx context.Context, tx *sql.Tx, id uuid.UUID, requestedBy string, cmdPayload []byte, reason string) error

	// GetByID is a read-only lookup outside any transaction.
	GetByID(ctx context.Context, id uuid.UUID) (model.PendingAdjustment, error)

	// MarkApproved atomically transitions status pending->approved — a
	// single conditional UPDATE (WHERE status='pending') that only succeeds
	// once, no matter how many concurrent approvers race for the same
	// pending adjustment (docs/plan/14 Task T2, decision K3 — same
	// mechanism as TransactionRepository.CloseOriginal). Returns rows
	// affected: 1 on success, 0 if already decided by someone else.
	MarkApproved(ctx context.Context, tx *sql.Tx, id uuid.UUID, approvedBy string) (int64, error)

	// MarkRejected is MarkApproved's mirror for rejection.
	MarkRejected(ctx context.Context, tx *sql.Tx, id uuid.UUID, approvedBy string) (int64, error)

	// MarkExecuted transitions approved->executed after Post() succeeds.
	MarkExecuted(ctx context.Context, tx *sql.Tx, id uuid.UUID, executedTxID uuid.UUID) error

	// MarkFailed transitions approved->failed when Post() itself errors —
	// NOT back to pending; a failed post needs a human decision to retry
	// (a brand new Create), not an automatic re-attempt (docs/plan/16 Task T1).
	MarkFailed(ctx context.Context, tx *sql.Tx, id uuid.UUID, errMsg string) error

	// List returns pending_adjustments rows filtered by status (empty =
	// all), newest first — for ops tooling. Read-only.
	List(ctx context.Context, status string, limit int) ([]model.PendingAdjustment, error)
}

type pendingAdjustmentRepo struct {
	db database.DatabaseSQL
}

func NewPendingAdjustmentRepository(db database.DatabaseSQL) PendingAdjustmentRepository {
	return &pendingAdjustmentRepo{db: db}
}

func (r *pendingAdjustmentRepo) Create(ctx context.Context, tx *sql.Tx, id uuid.UUID, requestedBy string, cmdPayload []byte, reason string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO pending_adjustments (id, requested_by, cmd_payload, reason, status, created_at)
		VALUES ($1, $2, $3, $4, 'pending', now())`,
		id, requestedBy, cmdPayload, reason,
	)
	if err != nil {
		return fmt.Errorf("create pending adjustment: %w", err)
	}
	return nil
}

func (r *pendingAdjustmentRepo) GetByID(ctx context.Context, id uuid.UUID) (model.PendingAdjustment, error) {
	var (
		pa           model.PendingAdjustment
		approvedBy   sql.NullString
		executedTxID sql.NullString
		errMsg       sql.NullString
		decidedAt    sql.NullTime
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT id, requested_by, approved_by, cmd_payload, reason, status,
		       executed_tx_id, error_message, created_at, decided_at
		FROM pending_adjustments
		WHERE id = $1`,
		id,
	).Scan(&pa.ID, &pa.RequestedBy, &approvedBy, &pa.CmdPayload, &pa.Reason, &pa.Status,
		&executedTxID, &errMsg, &pa.CreatedAt, &decidedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.PendingAdjustment{}, fmt.Errorf("%w: %s", apperror.ErrPendingAdjustmentNotFound, id)
	}
	if err != nil {
		return model.PendingAdjustment{}, fmt.Errorf("get pending adjustment: %w", err)
	}
	if approvedBy.Valid {
		pa.ApprovedBy = &approvedBy.String
	}
	if executedTxID.Valid {
		txID, err := uuid.Parse(executedTxID.String)
		if err != nil {
			return model.PendingAdjustment{}, fmt.Errorf("get pending adjustment: invalid stored executed_tx_id: %w", err)
		}
		pa.ExecutedTxID = &txID
	}
	if errMsg.Valid {
		pa.ErrorMessage = &errMsg.String
	}
	if decidedAt.Valid {
		pa.DecidedAt = &decidedAt.Time
	}
	return pa, nil
}

func (r *pendingAdjustmentRepo) MarkApproved(ctx context.Context, tx *sql.Tx, id uuid.UUID, approvedBy string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE pending_adjustments
		SET status = 'approved', approved_by = $1, decided_at = now()
		WHERE id = $2 AND status = 'pending'`,
		approvedBy, id,
	)
	if err != nil {
		return 0, fmt.Errorf("mark approved: %w", err)
	}
	return res.RowsAffected()
}

func (r *pendingAdjustmentRepo) MarkRejected(ctx context.Context, tx *sql.Tx, id uuid.UUID, approvedBy string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE pending_adjustments
		SET status = 'rejected', approved_by = $1, decided_at = now()
		WHERE id = $2 AND status = 'pending'`,
		approvedBy, id,
	)
	if err != nil {
		return 0, fmt.Errorf("mark rejected: %w", err)
	}
	return res.RowsAffected()
}

func (r *pendingAdjustmentRepo) MarkExecuted(ctx context.Context, tx *sql.Tx, id uuid.UUID, executedTxID uuid.UUID) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE pending_adjustments SET status = 'executed', executed_tx_id = $1 WHERE id = $2`,
		executedTxID, id,
	)
	if err != nil {
		return fmt.Errorf("mark executed: %w", err)
	}
	return nil
}

func (r *pendingAdjustmentRepo) MarkFailed(ctx context.Context, tx *sql.Tx, id uuid.UUID, errMsg string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE pending_adjustments SET status = 'failed', error_message = $1 WHERE id = $2`,
		errMsg, id,
	)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	return nil
}

func (r *pendingAdjustmentRepo) List(ctx context.Context, status string, limit int) ([]model.PendingAdjustment, error) {
	var rows *sql.Rows
	var err error
	if status == "" {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, requested_by, approved_by, cmd_payload, reason, status,
			       executed_tx_id, error_message, created_at, decided_at
			FROM pending_adjustments
			ORDER BY created_at DESC
			LIMIT $1`, limit)
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, requested_by, approved_by, cmd_payload, reason, status,
			       executed_tx_id, error_message, created_at, decided_at
			FROM pending_adjustments
			WHERE status = $1
			ORDER BY created_at DESC
			LIMIT $2`, status, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list pending adjustments: %w", err)
	}
	defer rows.Close()

	var out []model.PendingAdjustment
	for rows.Next() {
		var (
			pa           model.PendingAdjustment
			approvedBy   sql.NullString
			executedTxID sql.NullString
			errMsg       sql.NullString
			decidedAt    sql.NullTime
		)
		if err := rows.Scan(&pa.ID, &pa.RequestedBy, &approvedBy, &pa.CmdPayload, &pa.Reason, &pa.Status,
			&executedTxID, &errMsg, &pa.CreatedAt, &decidedAt); err != nil {
			return nil, fmt.Errorf("scan pending adjustment: %w", err)
		}
		if approvedBy.Valid {
			pa.ApprovedBy = &approvedBy.String
		}
		if executedTxID.Valid {
			txID, err := uuid.Parse(executedTxID.String)
			if err != nil {
				return nil, fmt.Errorf("scan pending adjustment: invalid stored executed_tx_id: %w", err)
			}
			pa.ExecutedTxID = &txID
		}
		if errMsg.Valid {
			pa.ErrorMessage = &errMsg.String
		}
		if decidedAt.Valid {
			pa.DecidedAt = &decidedAt.Time
		}
		out = append(out, pa)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending adjustments: %w", err)
	}
	return out, nil
}
