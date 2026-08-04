package model

import (
	"time"

	"github.com/google/uuid"
)

// FeeRuleVersion is an immutable proposed/approved pricing version. The
// legacy fee_rules table remains the active projection for compatibility with
// existing quote consumers; only approved versions may update that projection.
type FeeRuleVersion struct {
	ID              uuid.UUID
	RuleID          uuid.UUID
	Version         int64
	TxType          string
	Gateway         string
	Currency        string
	UserID          *uuid.UUID
	FlatMinorUnits  int64
	PercentBasisPts int64
	FeeGateway      string
	Enabled         bool
	Status          string
	CreatedBy       string
	SubmittedBy     string
	ApprovedBy      string
	RejectedBy      string
	DecisionReason  string
	EffectiveFrom   time.Time
	EffectiveUntil  *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
