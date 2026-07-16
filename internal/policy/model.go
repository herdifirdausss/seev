package policy

import (
	"time"

	"github.com/google/uuid"
)

// Limit is one row of policy_limits — either a per-type default (UserID
// nil) or a per-user override for one transaction type. All limit fields
// are nullable: nil means that dimension is unbounded.
type Limit struct {
	ID               uuid.UUID
	UserID           *uuid.UUID
	TransactionType  string
	MaxPerTx         *int64
	MaxDailyAmount   *int64
	MaxDailyCount    *int32
	MaxMonthlyAmount *int64
	Enabled          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
