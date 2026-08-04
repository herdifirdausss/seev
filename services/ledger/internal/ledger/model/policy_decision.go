package model

import (
	"time"

	"github.com/google/uuid"
)

// PolicyDecision is the immutable audit record written by the shared
// money-movement execution boundary before the low-level post begins.
type PolicyDecision struct {
	ID              uuid.UUID
	ActorID         uuid.UUID
	TenantID        uuid.UUID
	UserID          uuid.UUID
	Source          string
	CorrelationID   string
	RequestOrigin   string
	TransactionType string
	Currency        string
	AmountMinor     int64
	Allowed         bool
	Reason          string
	Detail          string
	EffectiveAt     time.Time
}
