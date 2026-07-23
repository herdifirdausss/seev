package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ScheduledTransaction is a recurring/deferred user transaction executed by
// a daily job (docs/roadmap/archive/19 Task T1). LastRunDate/LastError are
// informational only — the authoritative "has this run" answer is always
// the ledger's own idempotency key (sched:<id>:<run_date>), never a flag on
// this row.
type ScheduledTransaction struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	CmdPayload   json.RawMessage
	ScheduleKind string
	RunAtDate    time.Time
	DayOfMonth   *int
	Status       string
	LastRunDate  *time.Time
	LastError    *string
	CreatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
