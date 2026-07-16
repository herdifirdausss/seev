package repository

//go:generate mockgen -source=recon_repository.go -destination=recon_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

// insertItemsChunkSize caps how many recon_items rows one INSERT statement
// carries — a full batch (up to 50,000 rows, docs/plan/16 Task T2 step 3)
// would otherwise exceed Postgres's ~65535 bind-parameter limit (6 columns
// per row), so InsertItems loops in chunks within the caller's transaction
// instead of one giant statement (same concern as ledger_entry_repository's
// maxEntriesBatch, different scale).
const insertItemsChunkSize = 2000

// ReconRepository persists imported settlement batches and their per-row
// match outcomes (docs/plan/16 Task T2, decision K5). Write methods take a
// *sql.Tx — the caller (internal/ledger/service/recon) owns transaction
// boundaries, same pattern as every other repository in this module.
type ReconRepository interface {
	CreateBatch(ctx context.Context, tx *sql.Tx, batch model.ReconBatch) error
	UpdateBatchStatus(ctx context.Context, tx *sql.Tx, batchID uuid.UUID, status string) error
	GetBatch(ctx context.Context, id uuid.UUID) (model.ReconBatch, error)

	// ListBatches returns batches newest first, paginated (docs/plan/25
	// Task T5) — lets an operator find a batch's id without SQL before
	// drilling into GetBatchReport.
	ListBatches(ctx context.Context, limit, offset int) ([]model.ReconBatch, error)

	// InsertItems bulk-inserts CSV rows, pre-assigned MatchStatus
	// 'missing_internal' by the caller — RunMatcher promotes rows to
	// 'matched'/'amount_mismatch' afterward. Items must all share BatchID.
	InsertItems(ctx context.Context, tx *sql.Tx, items []model.ReconItem) error

	// RunMatcher is the two-statement set-based match (docs/plan/16 Task T2
	// step 4): first promotes existing batch items to matched/amount_mismatch
	// by joining ledger_transactions on (gateway, external_ref); items left
	// untouched stay 'missing_internal'. Second, inserts a NEW item for every
	// posted internal transaction on report_date with this gateway that no
	// batch row claimed — match_status 'missing_external'.
	RunMatcher(ctx context.Context, tx *sql.Tx, batchID uuid.UUID, gateway string, reportDate time.Time) error

	// GetCounts returns a count of items per match_status for a batch —
	// the report summary (docs/plan/16 Task T2 step 5).
	GetCounts(ctx context.Context, batchID uuid.UUID) (map[string]int, error)

	// ListItems returns items for a batch, newest first, optionally filtered
	// to one match_status (empty = all). Paginated.
	ListItems(ctx context.Context, batchID uuid.UUID, matchStatus string, limit, offset int) ([]model.ReconItem, error)

	GetItem(ctx context.Context, id uuid.UUID) (model.ReconItem, error)

	// MarkItemResolved atomically sets resolved_by_adjustment_id — guarded
	// by `WHERE resolved_by_adjustment_id IS NULL` so a double-resolve
	// (retry, or a race between two ops) can't create two pending
	// adjustments for the same discrepancy (docs/plan/14 Task T2 K3
	// pattern). Returns rows affected: 1 on success, 0 if already resolved.
	MarkItemResolved(ctx context.Context, tx *sql.Tx, itemID, adjustmentID uuid.UUID) (int64, error)
}

type reconRepo struct {
	db database.DatabaseSQL
}

func NewReconRepository(db database.DatabaseSQL) ReconRepository {
	return &reconRepo{db: db}
}

func (r *reconRepo) CreateBatch(ctx context.Context, tx *sql.Tx, batch model.ReconBatch) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO recon_batches (id, gateway, report_date, source_filename, row_count, status, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,now())`,
		batch.ID, batch.Gateway, batch.ReportDate, batch.SourceFilename, batch.RowCount, batch.Status, batch.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("create recon batch: %w", err)
	}
	return nil
}

func (r *reconRepo) UpdateBatchStatus(ctx context.Context, tx *sql.Tx, batchID uuid.UUID, status string) error {
	_, err := tx.ExecContext(ctx, `UPDATE recon_batches SET status = $1 WHERE id = $2`, status, batchID)
	if err != nil {
		return fmt.Errorf("update recon batch status: %w", err)
	}
	return nil
}

func (r *reconRepo) GetBatch(ctx context.Context, id uuid.UUID) (model.ReconBatch, error) {
	var b model.ReconBatch
	err := r.db.QueryRowContext(ctx, `
		SELECT id, gateway, report_date, source_filename, row_count, status, created_by, created_at
		FROM recon_batches WHERE id = $1`, id,
	).Scan(&b.ID, &b.Gateway, &b.ReportDate, &b.SourceFilename, &b.RowCount, &b.Status, &b.CreatedBy, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ReconBatch{}, fmt.Errorf("%w: %s", apperror.ErrReconBatchNotFound, id)
	}
	if err != nil {
		return model.ReconBatch{}, fmt.Errorf("get recon batch: %w", err)
	}
	return b, nil
}

func (r *reconRepo) ListBatches(ctx context.Context, limit, offset int) ([]model.ReconBatch, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, gateway, report_date, source_filename, row_count, status, created_by, created_at
		FROM recon_batches ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list recon batches: %w", err)
	}
	defer rows.Close()

	var out []model.ReconBatch
	for rows.Next() {
		var b model.ReconBatch
		if err := rows.Scan(&b.ID, &b.Gateway, &b.ReportDate, &b.SourceFilename, &b.RowCount, &b.Status, &b.CreatedBy, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan recon batch: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recon batches: %w", err)
	}
	return out, nil
}

func (r *reconRepo) InsertItems(ctx context.Context, tx *sql.Tx, items []model.ReconItem) error {
	const cols = 6
	for start := 0; start < len(items); start += insertItemsChunkSize {
		end := start + insertItemsChunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]

		args := make([]any, 0, len(chunk)*cols)
		parts := make([]string, 0, len(chunk))
		for i, it := range chunk {
			b := i*cols + 1
			parts = append(parts, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,now())", b, b+1, b+2, b+3, b+4, b+5))
			args = append(args, it.ID, it.BatchID, it.ExternalRef, it.Amount, []byte(it.Raw), it.MatchStatus)
		}

		q := "INSERT INTO recon_items (id, batch_id, external_ref, amount, raw, match_status, created_at) VALUES " +
			strings.Join(parts, ",")
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("batch insert recon items: %w", err)
		}
	}
	return nil
}

func (r *reconRepo) RunMatcher(ctx context.Context, tx *sql.Tx, batchID uuid.UUID, gateway string, reportDate time.Time) error {
	// Step A: promote CSV-imported rows to matched/amount_mismatch by
	// joining the internal ledger on (gateway, external_ref). Rows this
	// UPDATE doesn't touch keep their default 'missing_internal' — the
	// report claims an external_ref the ledger has no posted transaction
	// for.
	if _, err := tx.ExecContext(ctx, `
		UPDATE recon_items ri
		SET match_status = CASE WHEN lt.amount = ri.amount THEN 'matched' ELSE 'amount_mismatch' END,
		    matched_tx_id = lt.id
		FROM ledger_transactions lt
		WHERE ri.batch_id = $1
		  AND lt.gateway = $2
		  AND lt.external_ref = ri.external_ref
		  AND lt.status = 'posted'`,
		batchID, gateway,
	); err != nil {
		return fmt.Errorf("match internal to report: %w", err)
	}

	// Step B: the reverse direction — a posted internal transaction on
	// report_date with this gateway that no batch row claimed at all. Each
	// becomes its own new item, match_status='missing_external' (docs/plan/16
	// Task T2 step 4).
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO recon_items (id, batch_id, external_ref, amount, raw, match_status, matched_tx_id, created_at)
		SELECT gen_random_uuid(), $1, lt.external_ref, lt.amount, NULL, 'missing_external', lt.id, now()
		FROM ledger_transactions lt
		WHERE lt.gateway = $2
		  AND lt.external_ref IS NOT NULL
		  AND lt.status = 'posted'
		  AND lt.created_at::date = $3::timestamptz::date
		  AND NOT EXISTS (
		      SELECT 1 FROM recon_items ri2
		      WHERE ri2.batch_id = $1 AND ri2.external_ref = lt.external_ref
		  )`,
		batchID, gateway, reportDate,
	); err != nil {
		return fmt.Errorf("find internal-only transactions: %w", err)
	}

	return nil
}

func (r *reconRepo) GetCounts(ctx context.Context, batchID uuid.UUID) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT match_status, count(*) FROM recon_items WHERE batch_id = $1 GROUP BY match_status`,
		batchID,
	)
	if err != nil {
		return nil, fmt.Errorf("get recon counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scan recon counts: %w", err)
		}
		counts[status] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recon counts: %w", err)
	}
	return counts, nil
}

func (r *reconRepo) ListItems(ctx context.Context, batchID uuid.UUID, matchStatus string, limit, offset int) ([]model.ReconItem, error) {
	var rows *sql.Rows
	var err error
	if matchStatus == "" {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, batch_id, external_ref, amount, raw, match_status, matched_tx_id, resolved_by_adjustment_id, created_at
			FROM recon_items WHERE batch_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2 OFFSET $3`, batchID, limit, offset)
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, batch_id, external_ref, amount, raw, match_status, matched_tx_id, resolved_by_adjustment_id, created_at
			FROM recon_items WHERE batch_id = $1 AND match_status = $2
			ORDER BY created_at DESC, id DESC
			LIMIT $3 OFFSET $4`, batchID, matchStatus, limit, offset)
	}
	if err != nil {
		return nil, fmt.Errorf("list recon items: %w", err)
	}
	defer rows.Close()

	var out []model.ReconItem
	for rows.Next() {
		it, err := scanReconItemRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recon items: %w", err)
	}
	return out, nil
}

func (r *reconRepo) GetItem(ctx context.Context, id uuid.UUID) (model.ReconItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, batch_id, external_ref, amount, raw, match_status, matched_tx_id, resolved_by_adjustment_id, created_at
		FROM recon_items WHERE id = $1`, id,
	)
	it, err := scanReconItemRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ReconItem{}, fmt.Errorf("%w: %s", apperror.ErrReconItemNotFound, id)
	}
	if err != nil {
		return model.ReconItem{}, fmt.Errorf("get recon item: %w", err)
	}
	return it, nil
}

func (r *reconRepo) MarkItemResolved(ctx context.Context, tx *sql.Tx, itemID, adjustmentID uuid.UUID) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE recon_items SET resolved_by_adjustment_id = $1
		WHERE id = $2 AND resolved_by_adjustment_id IS NULL`,
		adjustmentID, itemID,
	)
	if err != nil {
		return 0, fmt.Errorf("mark recon item resolved: %w", err)
	}
	return res.RowsAffected()
}

// rowScanner abstracts *sql.Row and *sql.Rows for the shared scan logic
// below — both expose a compatible Scan method.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanReconItemRow(row rowScanner) (model.ReconItem, error) {
	var (
		it           model.ReconItem
		raw          []byte
		matchedTxID  sql.NullString
		resolvedByID sql.NullString
	)
	err := row.Scan(&it.ID, &it.BatchID, &it.ExternalRef, &it.Amount, &raw, &it.MatchStatus, &matchedTxID, &resolvedByID, &it.CreatedAt)
	if err != nil {
		return model.ReconItem{}, err
	}
	if len(raw) > 0 {
		it.Raw = raw
	}
	if matchedTxID.Valid {
		id, err := uuid.Parse(matchedTxID.String)
		if err != nil {
			return model.ReconItem{}, fmt.Errorf("scan recon item: invalid stored matched_tx_id: %w", err)
		}
		it.MatchedTxID = &id
	}
	if resolvedByID.Valid {
		id, err := uuid.Parse(resolvedByID.String)
		if err != nil {
			return model.ReconItem{}, fmt.Errorf("scan recon item: invalid stored resolved_by_adjustment_id: %w", err)
		}
		it.ResolvedByAdjustmentID = &id
	}
	return it, nil
}
