package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// DisbursementBatch is one imported CSV manifest (docs/roadmap/archive/19 Task T2).
type DisbursementBatch struct {
	ID             uuid.UUID
	SourceFilename string
	RowCount       int
	Status         string
	CreatedBy      string
	CreatedAt      time.Time
}

// DisbursementItem is one row of a batch — one user, one amount.
type DisbursementItem struct {
	ID         uuid.UUID
	BatchID    uuid.UUID
	ItemNo     int
	UserID     uuid.UUID
	Amount     decimal.Decimal
	Note       string
	Status     string
	Error      *string
	PostedTxID *uuid.UUID
}

// DisbursementImportRow is one CSV row before it's assigned an item number
// and inserted (docs/roadmap/archive/19 Task T2 step 2).
type DisbursementImportRow struct {
	UserID uuid.UUID
	Amount decimal.Decimal
	Note   string
}

// DisbursementBatchReport is a batch's header plus a count per item status
// and a page of items — GET /admin/disbursements/{id} (docs/roadmap/archive/19 Task
// T2 step 5, pola recon report).
type DisbursementBatchReport struct {
	Batch  DisbursementBatch
	Counts map[string]int
	Items  []DisbursementItem
}

// DisbursementRunResult reports one POST /admin/disbursements/{id}/run
// call's progress (docs/roadmap/archive/19 Task T2 step 6).
type DisbursementRunResult struct {
	Processed int
	Posted    int
	Failed    int
	// Done is true once no items remain to process — the batch's status
	// has been finalized ('completed' or 'completed_with_errors').
	Done bool
}
