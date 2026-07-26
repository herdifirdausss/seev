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
	"github.com/herdifirdausss/seev/pkg/cryptox"
	"github.com/herdifirdausss/seev/pkg/database"
)

// insertItemsChunkSize caps how many recon_items rows one INSERT statement
// carries — a full batch (up to 50,000 rows, docs/roadmap/archive/16 Task T2 step 3)
// would otherwise exceed Postgres's ~65535 bind-parameter limit (6 columns
// per row), so InsertItems loops in chunks within the caller's transaction
// instead of one giant statement (same concern as ledger_entry_repository's
// maxEntriesBatch, different scale).
const insertItemsChunkSize = 2000

// ReconRepository persists imported settlement batches and their per-row
// match outcomes (docs/roadmap/archive/16 Task T2, decision K5). Write methods take a
// *sql.Tx — the caller (internal/ledger/service/recon) owns transaction
// boundaries, same pattern as every other repository in this module.
type ReconRepository interface {
	CreateBatch(ctx context.Context, tx *sql.Tx, batch model.ReconBatch) error
	UpdateBatchStatus(ctx context.Context, tx *sql.Tx, batchID uuid.UUID, status string) error
	GetBatch(ctx context.Context, id uuid.UUID) (model.ReconBatch, error)

	// ListBatches returns batches newest first, paginated (docs/roadmap/archive/25
	// Task T5) — lets an operator find a batch's id without SQL before
	// drilling into GetBatchReport.
	ListBatches(ctx context.Context, limit, offset int) ([]model.ReconBatch, error)

	// InsertItems bulk-inserts CSV rows, pre-assigned MatchStatus
	// 'missing_internal' by the caller — RunMatcher promotes rows to
	// 'matched'/'amount_mismatch' afterward. Items must all share BatchID.
	InsertItems(ctx context.Context, tx *sql.Tx, items []model.ReconItem) error

	// RunMatcher is the two-statement set-based match (docs/roadmap/archive/16 Task T2
	// step 4): first promotes existing batch items to matched/amount_mismatch
	// by joining ledger_transactions on (gateway, external_ref); items left
	// untouched stay 'missing_internal'. Second, inserts a NEW item for every
	// posted internal transaction on report_date with this gateway that no
	// batch row claimed — match_status 'missing_external'.
	RunMatcher(ctx context.Context, tx *sql.Tx, batchID uuid.UUID, gateway string, reportDate time.Time) error

	// GetCounts returns a count of items per match_status for a batch —
	// the report summary (docs/roadmap/archive/16 Task T2 step 5).
	GetCounts(ctx context.Context, batchID uuid.UUID) (map[string]int, error)

	// ListItems returns items for a batch, newest first, optionally filtered
	// to one match_status (empty = all). Paginated.
	ListItems(ctx context.Context, batchID uuid.UUID, matchStatus string, limit, offset int) ([]model.ReconItem, error)

	GetItem(ctx context.Context, id uuid.UUID) (model.ReconItem, error)

	// MarkItemResolved atomically sets resolved_by_adjustment_id — guarded
	// by `WHERE resolved_by_adjustment_id IS NULL` so a double-resolve
	// (retry, or a race between two ops) can't create two pending
	// adjustments for the same discrepancy (docs/roadmap/archive/14 Task T2 K3
	// pattern). Returns rows affected: 1 on success, 0 if already resolved.
	MarkItemResolved(ctx context.Context, tx *sql.Tx, itemID, adjustmentID uuid.UUID) (int64, error)

}

type reconRepo struct {
	db   database.DatabaseSQL
	ring *cryptox.Ring
}

// NewReconRepository's ring is REQUIRED — docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
// "A8 T2.5b" (the contract migration): recon_batches.source_filename and
// recon_items.raw have no plaintext column anymore (migrations/ledger/000030),
// so every write needs the ring to function at all. A NULL ciphertext on
// an existing row is still a legitimate READ state (T2.6's own retention
// redaction nulls both after 90 days — see the scan methods below), which
// is why neither ciphertext column is NOT NULL.
func NewReconRepository(db database.DatabaseSQL, ring *cryptox.Ring) ReconRepository {
	if ring == nil {
		panic("ledger: NewReconRepository requires a non-nil cryptox ring")
	}
	return &reconRepo{db: db, ring: ring}
}

func sourceFilenameAAD(batchID uuid.UUID) cryptox.AAD {
	return cryptox.AAD{Service: "ledger", Table: "recon_batches", Column: "source_filename", RowID: batchID.String()}
}

func reconRawAAD(itemID uuid.UUID) cryptox.AAD {
	return cryptox.AAD{Service: "ledger", Table: "recon_items", Column: "raw", RowID: itemID.String()}
}

// redactedSourceFilenameMarker is what scanReconBatch returns for a row
// T2.6's own retention redaction already nulled source_filename_ciphertext
// on — the exact same "REDACTED" marker fn_retention_purge_recon_batches
// used to write into the (now-dropped) plaintext column directly.
const redactedSourceFilenameMarker = "REDACTED"

func (r *reconRepo) CreateBatch(ctx context.Context, tx *sql.Tx, batch model.ReconBatch) error {
	filenameCiphertext, err := r.ring.Seal(sourceFilenameAAD(batch.ID), []byte(batch.SourceFilename))
	if err != nil {
		return fmt.Errorf("encrypt recon source filename: %w", err)
	}
	v := r.ring.CurrentVersion()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO recon_batches (id, gateway, report_date, row_count, status, created_by, created_at,
			source_filename_ciphertext, source_filename_key_version)
		VALUES ($1,$2,$3,$4,$5,$6,now(),$7,$8)`,
		batch.ID, batch.Gateway, batch.ReportDate, batch.RowCount, batch.Status, batch.CreatedBy,
		filenameCiphertext, v,
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

const reconBatchColumns = `id, gateway, report_date, row_count, status, created_by, created_at,
	source_filename_ciphertext`

func (r *reconRepo) scanReconBatch(s rowScanner) (model.ReconBatch, error) {
	var b model.ReconBatch
	var filenameCiphertext []byte
	if err := s.Scan(&b.ID, &b.Gateway, &b.ReportDate, &b.RowCount, &b.Status, &b.CreatedBy, &b.CreatedAt,
		&filenameCiphertext); err != nil {
		return model.ReconBatch{}, err
	}
	if filenameCiphertext == nil {
		// T2.6's own retention redaction already nulled this — nothing
		// left to decrypt, same marker the pre-contract plaintext column
		// used to carry.
		b.SourceFilename = redactedSourceFilenameMarker
	} else {
		plain, err := r.ring.Open(sourceFilenameAAD(b.ID), filenameCiphertext)
		if err != nil {
			return model.ReconBatch{}, fmt.Errorf("decrypt recon source filename: %w", err)
		}
		b.SourceFilename = string(plain)
	}
	return b, nil
}

func (r *reconRepo) GetBatch(ctx context.Context, id uuid.UUID) (model.ReconBatch, error) {
	b, err := r.scanReconBatch(r.db.QueryRowContext(ctx, `
		SELECT `+reconBatchColumns+`
		FROM recon_batches WHERE id = $1`, id))
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
		SELECT `+reconBatchColumns+`
		FROM recon_batches ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list recon batches: %w", err)
	}
	defer rows.Close()

	var out []model.ReconBatch
	for rows.Next() {
		b, err := r.scanReconBatch(rows)
		if err != nil {
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
	const cols = 7
	for start := 0; start < len(items); start += insertItemsChunkSize {
		end := start + insertItemsChunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]

		args := make([]any, 0, len(chunk)*cols)
		parts := make([]string, 0, len(chunk))
		for i, it := range chunk {
			rawCiphertext, err := r.ring.Seal(reconRawAAD(it.ID), []byte(it.Raw))
			if err != nil {
				return fmt.Errorf("encrypt recon item raw: %w", err)
			}
			v := r.ring.CurrentVersion()
			b := i*cols + 1
			parts = append(parts, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,now(),$%d,$%d)", b, b+1, b+2, b+3, b+4, b+5, b+6))
			args = append(args, it.ID, it.BatchID, it.ExternalRef, it.Amount, it.MatchStatus, rawCiphertext, v)
		}

		q := "INSERT INTO recon_items (id, batch_id, external_ref, amount, match_status, created_at, raw_ciphertext, raw_key_version) VALUES " +
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
	// becomes its own new item, match_status='missing_external' (docs/roadmap/archive/16
	// Task T2 step 4).
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO recon_items (id, batch_id, external_ref, amount, match_status, matched_tx_id, created_at)
		SELECT gen_random_uuid(), $1, lt.external_ref, lt.amount, 'missing_external', lt.id, now()
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
			SELECT `+reconItemColumns+`
			FROM recon_items WHERE batch_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2 OFFSET $3`, batchID, limit, offset)
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT `+reconItemColumns+`
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
		it, err := r.scanReconItemRow(rows)
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
		SELECT `+reconItemColumns+`
		FROM recon_items WHERE id = $1`, id,
	)
	it, err := r.scanReconItemRow(row)
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

const reconItemColumns = `id, batch_id, external_ref, amount, match_status, matched_tx_id, resolved_by_adjustment_id, created_at,
	raw_ciphertext`

func (r *reconRepo) scanReconItemRow(row rowScanner) (model.ReconItem, error) {
	var (
		it            model.ReconItem
		matchedTxID   sql.NullString
		resolvedByID  sql.NullString
		rawCiphertext []byte
	)
	err := row.Scan(&it.ID, &it.BatchID, &it.ExternalRef, &it.Amount, &it.MatchStatus, &matchedTxID, &resolvedByID, &it.CreatedAt,
		&rawCiphertext)
	if err != nil {
		return model.ReconItem{}, err
	}
	// A NULL ciphertext here just means "no raw for this row" — either a
	// synthesized missing_external row (never had one) or one T2.6's own
	// retention redaction already cleared. Both are the same "nothing to
	// decrypt" state the pre-contract dual-read path already gave this.
	if rawCiphertext != nil {
		plain, err := r.ring.Open(reconRawAAD(it.ID), rawCiphertext)
		if err != nil {
			return model.ReconItem{}, fmt.Errorf("decrypt recon item raw: %w", err)
		}
		it.Raw = plain
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
