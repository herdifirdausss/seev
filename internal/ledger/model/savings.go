package model

import (
	"time"

	"github.com/google/uuid"
)

// SavingsConfig marks an account as interest-bearing (docs/plan/19 Task
// T3) — ops registers accounts explicitly; there is no magic
// pocket_code-prefix convention.
type SavingsConfig struct {
	AccountID     uuid.UUID
	AnnualRateBps int
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
