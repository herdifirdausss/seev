package model

import (
	"time"

	"github.com/google/uuid"
)

// ExecutionSubject is the ledger-side, fail-closed projection of auth state
// used when a command executes. It is not a replacement for Auth's identity
// database; it is the minimum state needed to stop a queued command after a
// disablement or KYC expiry.
type ExecutionSubject struct {
	UserID           uuid.UUID
	TenantID         uuid.UUID
	Status           string
	KYCLevel         int
	KYCVerifiedUntil *time.Time
	TenantStatus     string
	UpdatedAt        time.Time
}
