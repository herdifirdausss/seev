package repository

//go:generate mockgen -source=disbursement_repository.go -destination=disbursement_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

// DisbursementRepository persists batch disbursement manifests
// (docs/plan/19 Task T2). Write methods take a *sql.Tx — the caller
// (internal/ledger/service/disbursement) owns transaction boundaries.
type DisbursementRepository interface {
	// CreateBatchWithItems inserts the batch header and every item in ONE
	// transaction (docs/plan/19 Task T2 step 2) — a partially-imported
	// batch must never exist.
	CreateBatchWithItems(ctx context.Context, tx *sql.Tx, batch model.DisbursementBatch, items []model.DisbursementItem) error

	// GetBatch is a read-only lookup outside any transaction.
	GetBatch(ctx context.Context, id uuid.UUID) (model.DisbursementBatch, error)

	// UpdateBatchStatus transitions a batch to 'completed'/'completed_with_errors'
	// once every item has left 'pending' (or, when retryFailed, 'failed' too).
	UpdateBatchStatus(ctx context.Context, tx *sql.Tx, batchID uuid.UUID, status string) error

	// GetCounts returns a count of items per status for a batch (docs/plan/19
	// Task T2 step 5, report summary).
	GetCounts(ctx context.Context, batchID uuid.UUID) (map[string]int, error)

	// ListItems returns items for a batch, ordered by item_no, optionally
	// filtered to one status (empty = all). Paginated.
	ListItems(ctx context.Context, batchID uuid.UUID, status string, limit, offset int) ([]model.DisbursementItem, error)

	// ListItemsToProcess selects up to limit items still needing a Post
	// attempt — 'pending' always, plus 'failed' when includeFailed is true
	// (the ?retry_failed=true flag, docs/plan/19 Task T2 step 4) — ordered
	// by item_no for deterministic, resumable progress across calls.
	ListItemsToProcess(ctx context.Context, batchID uuid.UUID, includeFailed bool, limit int) ([]model.DisbursementItem, error)

	// MarkItemPosted and MarkItemFailed record one item's outcome after a
	// Post attempt — each item's own small transaction (not the whole
	// batch's), so a crash mid-run only loses the in-flight item's own
	// write, never rolls back items already recorded (docs/plan/19 Task T2
	// step 6, the resumable-by-design N-per-call model).
	MarkItemPosted(ctx context.Context, tx *sql.Tx, itemID, txID uuid.UUID) error
	MarkItemFailed(ctx context.Context, tx *sql.Tx, itemID uuid.UUID, errMsg string) error
}

type disbursementRepo struct {
	db database.DatabaseSQL
}

func NewDisbursementRepository(db database.DatabaseSQL) DisbursementRepository {
	return &disbursementRepo{db: db}
}

func (r *disbursementRepo) CreateBatchWithItems(ctx context.Context, tx *sql.Tx, batch model.DisbursementBatch, items []model.DisbursementItem) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO disbursement_batches (id, source_filename, row_count, status, created_by, created_at)
		VALUES ($1, $2, $3, 'processing', $4, now())`,
		batch.ID, batch.SourceFilename, batch.RowCount, batch.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("create disbursement batch: %w", err)
	}
	for _, it := range items {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO disbursement_items (id, batch_id, item_no, user_id, amount, note, status)
			VALUES ($1, $2, $3, $4, $5, $6, 'pending')`,
			it.ID, it.BatchID, it.ItemNo, it.UserID, it.Amount.IntPart(), it.Note,
		)
		if err != nil {
			return fmt.Errorf("insert disbursement item %d: %w", it.ItemNo, err)
		}
	}
	return nil
}

func (r *disbursementRepo) GetBatch(ctx context.Context, id uuid.UUID) (model.DisbursementBatch, error) {
	var b model.DisbursementBatch
	err := r.db.QueryRowContext(ctx, `
		SELECT id, source_filename, row_count, status, created_by, created_at
		FROM disbursement_batches WHERE id = $1`, id,
	).Scan(&b.ID, &b.SourceFilename, &b.RowCount, &b.Status, &b.CreatedBy, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DisbursementBatch{}, fmt.Errorf("%w: %s", apperror.ErrDisbursementBatchNotFound, id)
	}
	if err != nil {
		return model.DisbursementBatch{}, fmt.Errorf("get disbursement batch: %w", err)
	}
	return b, nil
}

func (r *disbursementRepo) UpdateBatchStatus(ctx context.Context, tx *sql.Tx, batchID uuid.UUID, status string) error {
	_, err := tx.ExecContext(ctx, `UPDATE disbursement_batches SET status = $1 WHERE id = $2`, status, batchID)
	if err != nil {
		return fmt.Errorf("update disbursement batch status: %w", err)
	}
	return nil
}

func (r *disbursementRepo) GetCounts(ctx context.Context, batchID uuid.UUID) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT status, count(*) FROM disbursement_items WHERE batch_id = $1 GROUP BY status`, batchID)
	if err != nil {
		return nil, fmt.Errorf("get disbursement item counts: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scan disbursement item count: %w", err)
		}
		counts[status] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate disbursement item counts: %w", err)
	}
	return counts, nil
}

func scanDisbursementItem(scan func(dest ...any) error) (model.DisbursementItem, error) {
	var (
		it         model.DisbursementItem
		amount     int64
		errMsg     sql.NullString
		postedTxID sql.NullString
	)
	err := scan(&it.ID, &it.BatchID, &it.ItemNo, &it.UserID, &amount, &it.Note, &it.Status, &errMsg, &postedTxID)
	if err != nil {
		return model.DisbursementItem{}, err
	}
	it.Amount = decimal.NewFromInt(amount)
	if errMsg.Valid {
		it.Error = &errMsg.String
	}
	if postedTxID.Valid {
		id, err := uuid.Parse(postedTxID.String)
		if err != nil {
			return model.DisbursementItem{}, fmt.Errorf("invalid stored posted_tx_id: %w", err)
		}
		it.PostedTxID = &id
	}
	return it, nil
}

const disbursementItemColumns = `id, batch_id, item_no, user_id, amount, note, status, error, posted_tx_id`

func (r *disbursementRepo) ListItems(ctx context.Context, batchID uuid.UUID, status string, limit, offset int) ([]model.DisbursementItem, error) {
	var rows *sql.Rows
	var err error
	if status == "" {
		rows, err = r.db.QueryContext(ctx, `SELECT `+disbursementItemColumns+`
			FROM disbursement_items WHERE batch_id = $1 ORDER BY item_no LIMIT $2 OFFSET $3`, batchID, limit, offset)
	} else {
		rows, err = r.db.QueryContext(ctx, `SELECT `+disbursementItemColumns+`
			FROM disbursement_items WHERE batch_id = $1 AND status = $2 ORDER BY item_no LIMIT $3 OFFSET $4`, batchID, status, limit, offset)
	}
	if err != nil {
		return nil, fmt.Errorf("list disbursement items: %w", err)
	}
	defer rows.Close()
	return scanDisbursementItemRows(rows)
}

func (r *disbursementRepo) ListItemsToProcess(ctx context.Context, batchID uuid.UUID, includeFailed bool, limit int) ([]model.DisbursementItem, error) {
	statuses := []string{"pending"}
	if includeFailed {
		statuses = append(statuses, "failed")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+disbursementItemColumns+`
		FROM disbursement_items WHERE batch_id = $1 AND status = ANY($2) ORDER BY item_no LIMIT $3`,
		batchID, statuses, limit)
	if err != nil {
		return nil, fmt.Errorf("list disbursement items to process: %w", err)
	}
	defer rows.Close()
	return scanDisbursementItemRows(rows)
}

func scanDisbursementItemRows(rows *sql.Rows) ([]model.DisbursementItem, error) {
	var out []model.DisbursementItem
	for rows.Next() {
		it, err := scanDisbursementItem(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan disbursement item: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate disbursement items: %w", err)
	}
	return out, nil
}

func (r *disbursementRepo) MarkItemPosted(ctx context.Context, tx *sql.Tx, itemID, txID uuid.UUID) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE disbursement_items SET status = 'posted', posted_tx_id = $1, error = NULL WHERE id = $2`,
		txID, itemID,
	)
	if err != nil {
		return fmt.Errorf("mark disbursement item posted: %w", err)
	}
	return nil
}

func (r *disbursementRepo) MarkItemFailed(ctx context.Context, tx *sql.Tx, itemID uuid.UUID, errMsg string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE disbursement_items SET status = 'failed', error = $1 WHERE id = $2`,
		errMsg, itemID,
	)
	if err != nil {
		return fmt.Errorf("mark disbursement item failed: %w", err)
	}
	return nil
}
