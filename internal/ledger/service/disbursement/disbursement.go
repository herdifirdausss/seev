// Package disbursement implements batch payouts (docs/plan/19 Task T2,
// decision S3 butir 2): one CSV manifest -> many `disbursement` postings,
// executed in bounded N-item slices via repeated calls to Run rather than
// synchronously in the import request (a 50,000-row Post loop would blow
// past the internal router's request timeout) or via a new background
// worker (docs/plan/13 K5 "jangan tambah worker" — this package adds none).
// Each item's idempotency key ("batch:<batch_id>:<item_no>") makes Run
// naturally resumable: calling it again after a partial run, a crash, or
// an operator's deliberate retry only ever processes items still
// 'pending' (or 'failed', with retryFailed) — an already-'posted' item is
// never re-selected, so double-posting is structurally impossible, not
// just discouraged. There is no separate "resume" code path — resuming IS
// calling Run again.
package disbursement

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// maxImportRows mirrors service/recon's own cap (docs/plan/16 Task T2) — a
// batch larger than this must be split into multiple imports.
const maxImportRows = 50_000

// defaultMaxItemsPerRun bounds how many items a single Run call processes
// (docs/plan/19 Task T2 step 6) — keeps each call comfortably inside the
// internal router's request timeout regardless of batch size; the caller
// (ops tooling/script) calls Run repeatedly until Done is true.
const defaultMaxItemsPerRun = 500

// DatabaseSQL is the thin interface over the connection pool this service
// needs — mirrors adjustments/recon/schedule's own narrow redefinitions.
type DatabaseSQL interface {
	WithTx(ctx context.Context, opts *sql.TxOptions, fn func(tx *sql.Tx) error) error
}

// Poster is the subset of ledgerhandle.Service this package needs.
type Poster interface {
	Handle(ctx context.Context, cmd processors.Command) error
}

type Service struct {
	db             DatabaseSQL
	repo           repository.DisbursementRepository
	txRepo         repository.TransactionRepository
	poster         Poster
	maxItemsPerRun int
}

type Option func(*Service)

// WithMaxItemsPerRun overrides defaultMaxItemsPerRun — production never
// needs this; integration tests use it to prove pagination across
// multiple Run calls without importing hundreds of rows (pola
// policy.Engine's WithCacheTTL, docs/plan/17 Task T1).
func WithMaxItemsPerRun(n int) Option {
	return func(s *Service) { s.maxItemsPerRun = n }
}

func New(db DatabaseSQL, repo repository.DisbursementRepository, txRepo repository.TransactionRepository, poster Poster, opts ...Option) *Service {
	s := &Service{db: db, repo: repo, txRepo: txRepo, poster: poster, maxItemsPerRun: defaultMaxItemsPerRun}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Import validates and persists a new batch — item_no is assigned as the
// row's 1-based position in the file. Does NOT post anything; call Run to
// start (or resume) processing.
func (s *Service) Import(ctx context.Context, filename string, rows []model.DisbursementImportRow, createdBy string) (uuid.UUID, error) {
	if len(rows) == 0 {
		return uuid.Nil, fmt.Errorf("%w: batch has no rows", apperror.ErrValidation)
	}
	if len(rows) > maxImportRows {
		return uuid.Nil, fmt.Errorf("%w: %d rows exceeds the %d-row cap per batch — split the file", apperror.ErrCSVTooManyRows, len(rows), maxImportRows)
	}
	if createdBy == "" {
		return uuid.Nil, fmt.Errorf("%w: created_by (caller identity) is required", apperror.ErrValidation)
	}
	for i, r := range rows {
		if r.UserID == uuid.Nil {
			return uuid.Nil, fmt.Errorf("%w: row %d has an empty user_id", apperror.ErrValidation, i+1)
		}
		if !r.Amount.IsPositive() || !r.Amount.Equal(r.Amount.Truncate(0)) {
			return uuid.Nil, fmt.Errorf("%w: row %d (user_id=%s) has a non-integral or non-positive amount", apperror.ErrValidation, i+1, r.UserID)
		}
	}

	batchID := generalutil.NewV7()
	batch := model.DisbursementBatch{ID: batchID, SourceFilename: filename, RowCount: len(rows), CreatedBy: createdBy}
	items := make([]model.DisbursementItem, len(rows))
	for i, r := range rows {
		items[i] = model.DisbursementItem{
			ID: generalutil.NewV7(), BatchID: batchID, ItemNo: i + 1,
			UserID: r.UserID, Amount: r.Amount, Note: r.Note,
		}
	}

	err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return s.repo.CreateBatchWithItems(ctx, tx, batch, items)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return batchID, nil
}

// Run processes up to maxItemsPerRun items still needing a Post attempt.
// Call repeatedly until Done is true. retryFailed additionally reprocesses
// items already marked 'failed' (docs/plan/19 Task T2 step 4) — omit it to
// only advance items that have never been attempted.
func (s *Service) Run(ctx context.Context, batchID uuid.UUID, retryFailed bool) (model.DisbursementRunResult, error) {
	batch, err := s.repo.GetBatch(ctx, batchID)
	if err != nil {
		return model.DisbursementRunResult{}, err
	}

	items, err := s.repo.ListItemsToProcess(ctx, batchID, retryFailed, s.maxItemsPerRun)
	if err != nil {
		return model.DisbursementRunResult{}, fmt.Errorf("disbursement: list items to process: %w", err)
	}

	var result model.DisbursementRunResult
	for _, item := range items {
		result.Processed++
		key := disbursementIdempotencyKey(batchID, item.ItemNo)
		cmd := processors.Command{
			IdempotencyKey: key,
			Type:           "disbursement",
			Amount:         item.Amount,
			UserID:         item.UserID,
			Metadata: map[string]any{
				"batch_id": batchID.String(),
				"item_no":  fmt.Sprintf("%d", item.ItemNo),
			},
		}
		postErr := s.poster.Handle(ctx, cmd)

		var markErr error
		if postErr == nil {
			posted, lookupErr := s.txRepo.GetByIdempotencyKey(ctx, key, nil)
			if lookupErr != nil {
				markErr = fmt.Errorf("posted but lookup failed: %w", lookupErr)
			} else {
				markErr = s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
					return s.repo.MarkItemPosted(ctx, tx, item.ID, posted.ID)
				})
			}
		} else {
			markErr = s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
				return s.repo.MarkItemFailed(ctx, tx, item.ID, postErr.Error())
			})
		}
		if markErr != nil {
			// The item's own outcome couldn't be recorded — leave its
			// status as-is (still 'pending'/'failed') so the NEXT Run call
			// reconsiders it; whatever Post did or didn't do, its own
			// idempotency key makes reconsidering it safe either way
			// (docs/plan/19 Task T2's core locked pattern).
			result.Failed++
			continue
		}
		if postErr == nil {
			result.Posted++
		} else {
			result.Failed++
		}
	}

	remaining, err := s.repo.ListItemsToProcess(ctx, batchID, retryFailed, 1)
	if err != nil {
		return result, fmt.Errorf("disbursement: check remaining: %w", err)
	}
	if len(remaining) == 0 {
		counts, err := s.repo.GetCounts(ctx, batchID)
		if err != nil {
			return result, fmt.Errorf("disbursement: get counts: %w", err)
		}
		status := "completed"
		if counts["failed"] > 0 {
			status = "completed_with_errors"
		}
		if batch.Status != status {
			if err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
				return s.repo.UpdateBatchStatus(ctx, tx, batchID, status)
			}); err != nil {
				return result, fmt.Errorf("disbursement: update batch status: %w", err)
			}
		}
		result.Done = true
	}
	return result, nil
}

// GetReport returns a batch's header, a count per item status, and a page
// of items — optionally filtered to one status.
func (s *Service) GetReport(ctx context.Context, batchID uuid.UUID, status string, limit, offset int) (model.DisbursementBatchReport, error) {
	batch, err := s.repo.GetBatch(ctx, batchID)
	if err != nil {
		return model.DisbursementBatchReport{}, err
	}
	counts, err := s.repo.GetCounts(ctx, batchID)
	if err != nil {
		return model.DisbursementBatchReport{}, fmt.Errorf("disbursement: get counts: %w", err)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	items, err := s.repo.ListItems(ctx, batchID, status, limit, offset)
	if err != nil {
		return model.DisbursementBatchReport{}, fmt.Errorf("disbursement: list items: %w", err)
	}
	return model.DisbursementBatchReport{Batch: batch, Counts: counts, Items: items}, nil
}

func disbursementIdempotencyKey(batchID uuid.UUID, itemNo int) string {
	return fmt.Sprintf("batch:%s:%d", batchID, itemNo)
}
