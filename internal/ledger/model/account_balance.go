package model

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type AccountBalance struct {
	AccountID uuid.UUID
	Currency  string
	Balance   decimal.Decimal
	Status    string
	Type      string
	// AllowNegative mirrors account_balances.allow_negative — true only for
	// system accounts by design (settlement, adjustment, chargeback; see
	// migrations/000001_ledger_core.up.sql). Determines whether an account
	// is locked with FOR UPDATE (false) or updated via atomic delta-apply
	// (true) — see docs/plan/11 Task T1.
	AllowNegative bool
}
