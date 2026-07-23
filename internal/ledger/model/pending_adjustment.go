package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// PendingAdjustment is a row from pending_adjustments — the maker-checker
// workflow for manual balance adjustments (docs/roadmap/archive/16 Task T1, decision
// K8). CmdPayload is kept as raw JSON at this layer; only
// internal/ledger/service/adjustments knows its shape.
type PendingAdjustment struct {
	ID           uuid.UUID
	RequestedBy  string
	ApprovedBy   *string
	CmdPayload   json.RawMessage
	Reason       string
	Status       string
	ExecutedTxID *uuid.UUID
	ErrorMessage *string
	CreatedAt    time.Time
	DecidedAt    *time.Time
}
