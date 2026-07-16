// Package recon implements external settlement reconciliation (docs/plan/16
// Task T2, decision K5): import a gateway's settlement report, match it
// set-based against the internal ledger, and — for anything that doesn't
// match — let an ops identity request a correction that only moves money
// once a SECOND identity approves it (reusing the maker-checker governance
// from internal/ledger/service/adjustments, docs/plan/16 Task T1).
package recon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// maxImportRows caps a single CSV import (docs/plan/16 Task T2 step 3) — a
// larger settlement file must be split by the caller, never silently
// processed partially.
const maxImportRows = 50_000

// DatabaseSQL is the thin interface over the connection pool this service
// needs — mirrors every other service package's own narrow redefinition.
type DatabaseSQL interface {
	WithTx(ctx context.Context, opts *sql.TxOptions, fn func(tx *sql.Tx) error) error
}

// AdjustmentCreator is the subset of adjustments.Service this package
// needs — creating a pending adjustment when a discrepancy is resolved.
// Create() never moves money by itself (docs/plan/16 Task T1) — that only
// happens once a second identity calls Approve, which is exactly the
// property K5 step 5 requires ("uang tidak bergerak tanpa approve manusia
// kedua").
type AdjustmentCreator interface {
	Create(ctx context.Context, requestedBy, adjType string, amount decimal.Decimal, targetUserID uuid.UUID, metadata map[string]any, reason string) (uuid.UUID, error)
}

// ImportRow is model.ReconImportRow's alias in this package — parsing/
// streaming lives in the transport handler (docs/plan/16 Task T2 step 3);
// this package only validates and persists already-parsed rows.
type ImportRow = model.ReconImportRow

type Service struct {
	db   DatabaseSQL
	repo repository.ReconRepository
	adj  AdjustmentCreator
}

func New(db DatabaseSQL, repo repository.ReconRepository, adj AdjustmentCreator) *Service {
	return &Service{db: db, repo: repo, adj: adj}
}

// ImportBatch validates, persists, and matches one settlement report in a
// single DB transaction (docs/plan/16 Task T2 steps 3-4): create the batch
// header, bulk-insert every row as 'missing_internal', run the set-based
// matcher to promote matches/mismatches and discover internal-only
// transactions, then mark the batch completed.
func (s *Service) ImportBatch(ctx context.Context, gateway string, reportDate time.Time, filename string, rows []ImportRow, createdBy string) (uuid.UUID, error) {
	if !constant.ValidGateways[gateway] {
		return uuid.Nil, fmt.Errorf("%w: unknown gateway %q", apperror.ErrValidation, gateway)
	}
	if len(rows) == 0 {
		return uuid.Nil, fmt.Errorf("%w: batch has no rows", apperror.ErrValidation)
	}
	if len(rows) > maxImportRows {
		return uuid.Nil, fmt.Errorf("%w: %d rows exceeds the %d-row cap per batch — split the file", apperror.ErrCSVTooManyRows, len(rows), maxImportRows)
	}
	if createdBy == "" {
		return uuid.Nil, fmt.Errorf("%w: created_by (caller identity) is required", apperror.ErrValidation)
	}

	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		if r.ExternalRef == "" {
			return uuid.Nil, fmt.Errorf("%w: row has empty external_ref", apperror.ErrValidation)
		}
		if seen[r.ExternalRef] {
			return uuid.Nil, fmt.Errorf("%w: duplicate external_ref %q in file", apperror.ErrValidation, r.ExternalRef)
		}
		seen[r.ExternalRef] = true
		if !r.Amount.IsPositive() || !r.Amount.Equal(r.Amount.Truncate(0)) {
			return uuid.Nil, fmt.Errorf("%w: external_ref %q has a non-integral or non-positive amount", apperror.ErrValidation, r.ExternalRef)
		}
	}

	batchID := generalutil.NewV7()
	items := make([]model.ReconItem, len(rows))
	for i, r := range rows {
		raw, err := json.Marshal(map[string]string{
			"external_ref": r.ExternalRef, "amount": r.Amount.String(), "settled_at": r.SettledAt,
		})
		if err != nil {
			return uuid.Nil, fmt.Errorf("marshal raw row: %w", err)
		}
		items[i] = model.ReconItem{
			ID: generalutil.NewV7(), BatchID: batchID, ExternalRef: r.ExternalRef,
			Amount: r.Amount, Raw: raw, MatchStatus: "missing_internal",
		}
	}

	err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		batch := model.ReconBatch{
			ID: batchID, Gateway: gateway, ReportDate: reportDate,
			SourceFilename: filename, RowCount: len(rows), Status: "processing", CreatedBy: createdBy,
		}
		if err := s.repo.CreateBatch(ctx, tx, batch); err != nil {
			return err
		}
		if err := s.repo.InsertItems(ctx, tx, items); err != nil {
			return err
		}
		if err := s.repo.RunMatcher(ctx, tx, batchID, gateway, reportDate); err != nil {
			return err
		}
		return s.repo.UpdateBatchStatus(ctx, tx, batchID, "completed")
	})
	if err != nil {
		return uuid.Nil, err
	}
	return batchID, nil
}

// defaultReportLimit/maxReportLimit bound GetBatchReport's item page —
// same pattern as adjustments.Service.List.
const (
	defaultReportLimit = 100
	maxReportLimit     = 500
)

// GetBatchReport returns the batch header, a count per match_status, and a
// page of items — optionally filtered to one match_status (docs/plan/16
// Task T2 step 5).
func (s *Service) GetBatchReport(ctx context.Context, batchID uuid.UUID, matchStatus string, limit, offset int) (model.ReconBatchReport, error) {
	batch, err := s.repo.GetBatch(ctx, batchID)
	if err != nil {
		return model.ReconBatchReport{}, err
	}
	counts, err := s.repo.GetCounts(ctx, batchID)
	if err != nil {
		return model.ReconBatchReport{}, err
	}
	if limit <= 0 || limit > maxReportLimit {
		limit = defaultReportLimit
	}
	if offset < 0 {
		offset = 0
	}
	items, err := s.repo.ListItems(ctx, batchID, matchStatus, limit, offset)
	if err != nil {
		return model.ReconBatchReport{}, err
	}
	return model.ReconBatchReport{Batch: batch, Counts: counts, Items: items}, nil
}

// ListBatches returns batches newest first, paginated (docs/plan/25 Task
// T5) — lets an operator find a batch's id without SQL before drilling
// into GetBatchReport.
func (s *Service) ListBatches(ctx context.Context, limit, offset int) ([]model.ReconBatch, error) {
	if limit <= 0 || limit > maxReportLimit {
		limit = defaultReportLimit
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListBatches(ctx, limit, offset)
}

// resolvableAdjustmentTypes are the only pending-adjustment types a recon
// resolution may request — both target a gateway's suspense account, never
// a user (docs/plan/16 Task T2, decision K5).
var resolvableAdjustmentTypes = map[string]bool{
	"adjustment_suspense_credit": true,
	"adjustment_suspense_debit":  true,
}

// ResolveItem requests a correction for a non-matched recon item — it does
// NOT move any money itself, only creates a pending adjustment (K5 step 5:
// "uang tidak bergerak tanpa approve manusia kedua"). adjType must be
// adjustment_suspense_credit or adjustment_suspense_debit — the caller (ops,
// after investigating the discrepancy) decides direction and amount; this
// package does not infer one from match_status, since that inference is
// exactly the judgment call maker-checker exists to require a human make.
//
// The atomic MarkItemResolved guard runs AFTER AdjustmentCreator.Create
// (which itself only writes a pending_adjustments row, no money movement)
// — if two resolve requests race for the same item, the loser's freshly
// created pending adjustment is orphaned (never approved automatically).
// This is the same accepted multi-transaction tradeoff documented on
// adjustments.Service.Approve: a human must reject the orphaned adjustment,
// but no money-safety invariant is at risk because Create() alone never
// posts anything.
func (s *Service) ResolveItem(ctx context.Context, itemID uuid.UUID, requestedBy, adjType string, amount decimal.Decimal, reason string) (uuid.UUID, error) {
	if !resolvableAdjustmentTypes[adjType] {
		return uuid.Nil, fmt.Errorf("%w: type must be adjustment_suspense_credit or adjustment_suspense_debit", apperror.ErrValidation)
	}

	item, err := s.repo.GetItem(ctx, itemID)
	if err != nil {
		return uuid.Nil, err
	}
	if item.ResolvedByAdjustmentID != nil {
		return uuid.Nil, apperror.NewBizErr(apperror.ErrReconItemAlreadyResolved,
			fmt.Sprintf("recon item %s already resolved by adjustment %s", itemID, *item.ResolvedByAdjustmentID))
	}
	batch, err := s.repo.GetBatch(ctx, item.BatchID)
	if err != nil {
		return uuid.Nil, err
	}

	if amount.IsZero() {
		amount = item.Amount
	}
	if reason == "" {
		reason = fmt.Sprintf("recon resolution: batch=%s external_ref=%s match_status=%s recon_item=%s",
			item.BatchID, item.ExternalRef, item.MatchStatus, item.ID)
	} else {
		reason = fmt.Sprintf("%s (recon_item=%s)", reason, item.ID)
	}

	metadata := map[string]any{"gateway": batch.Gateway}
	adjustmentID, err := s.adj.Create(ctx, requestedBy, adjType, amount, uuid.Nil, metadata, reason)
	if err != nil {
		return uuid.Nil, err
	}

	var rows int64
	err = s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var err error
		rows, err = s.repo.MarkItemResolved(ctx, tx, itemID, adjustmentID)
		return err
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("pending adjustment %s created but mark-resolved failed: %w", adjustmentID, err)
	}
	if rows == 0 {
		return uuid.Nil, apperror.NewBizErr(apperror.ErrReconItemAlreadyResolved,
			fmt.Sprintf("recon item %s was resolved concurrently — pending adjustment %s was created but is orphaned; reject it", itemID, adjustmentID))
	}
	return adjustmentID, nil
}
