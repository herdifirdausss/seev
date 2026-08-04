package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ChargebackDispute is one card-network dispute case against a posted
// charge — the case-management record services/ledger/migrations/000035_chargeback_
// disputes.up.sql added on top of the chargeback processor's own money-
// movement transaction (services/ledger/internal/processors/chargeback.go), which
// this table never touches directly.
type ChargebackDispute struct {
	ID               uuid.UUID
	OriginalTxID     uuid.UUID
	ChargebackTxID   *uuid.UUID
	DisputeRef       string
	CardNetwork      string
	ReasonCode       string
	Amount           decimal.Decimal
	Currency         string
	Status           string
	EvidenceDueAt    *time.Time
	EvidenceRef      string
	ResolvedAt       *time.Time
	ResolvedBy       string
	ResolutionReason string
	CreatedBy        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ChargebackDisputeStatusChange is one row of a dispute's append-only
// status-transition audit trail (services/ledger/migrations/000037) — mirrors
// auth's kyc_level_changes shape: every transition, who made it, and why.
type ChargebackDisputeStatusChange struct {
	ID         uuid.UUID
	DisputeID  uuid.UUID
	FromStatus string
	ToStatus   string
	Reason     string
	ChangedBy  string
	CreatedAt  time.Time
}
