package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ReconBatch is one imported settlement report (docs/roadmap/archive/16 Task T2,
// decision K5) — one CSV file for one gateway's one report_date.
type ReconBatch struct {
	ID             uuid.UUID
	Gateway        string
	ReportDate     time.Time
	SourceFilename string
	RowCount       int
	Status         string
	CreatedBy      string
	CreatedAt      time.Time
}

// ReconItem is one external_ref's match outcome within a batch — either
// imported directly from the CSV (match_status starts 'missing_internal'
// until the matcher runs) or inserted BY the matcher itself for an internal
// transaction the report never mentioned (match_status='missing_external').
type ReconItem struct {
	ID                     uuid.UUID
	BatchID                uuid.UUID
	ExternalRef            string
	Amount                 decimal.Decimal
	Raw                    json.RawMessage
	MatchStatus            string
	MatchedTxID            *uuid.UUID
	ResolvedByAdjustmentID *uuid.UUID
	CreatedAt              time.Time
}

// ReconBatchReport is the GET /admin/recon/batches/{id} response shape:
// the batch header, a count per match_status, and the (paginated) list of
// items the caller asked to see.
type ReconBatchReport struct {
	Batch  ReconBatch
	Counts map[string]int
	Items  []ReconItem
}

// ReconImportRow is one parsed CSV line from a settlement report — parsing
// lives in the transport layer (streaming encoding/csv), validation and
// persistence in service/recon (docs/roadmap/archive/16 Task T2 step 3).
type ReconImportRow struct {
	ExternalRef string
	Amount      decimal.Decimal
	SettledAt   string
}
