package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// LedgerTransaction is the public read DTO for a ledger_transactions header
// row, returned by GetTransaction read APIs.
type LedgerTransaction struct {
	ID                   uuid.UUID
	IdempotencyKey       string
	IdempotencyScope     string
	Type                 string
	Status               string
	Amount               decimal.Decimal
	Currency             string
	SourceAccountID      uuid.UUID
	DestinationAccountID uuid.UUID
	ErrorMessage         string
	// ExternalRef/Gateway correlate this transaction to a payment gateway's
	// own settlement report (docs/roadmap/archive/16 Task T2) — informative, like
	// Source/DestinationAccountID; empty for transaction types that never
	// carry a "gateway"/"external_ref" metadata key.
	ExternalRef string
	Gateway     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
