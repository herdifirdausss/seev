package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// FeeRule is the persisted pricing configuration selected by
// feepolicy.Policy.Resolve.
type FeeRule struct {
	ID              uuid.UUID
	TxType          string
	Gateway         string
	Currency        string
	UserID          *uuid.UUID
	FlatMinorUnits  int64
	PercentBasisPts int64
	FeeGateway      string
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// FeeQuote is a fee locked in for a specific (user, tx_type, currency,
// amount) combination until ExpiresAt, single-use.
type FeeQuote struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	TransactionType string
	Gateway         string
	Currency        string
	Amount          decimal.Decimal
	FeeAmount       decimal.Decimal
	FeeGateway      string
	ExpiresAt       time.Time
}
