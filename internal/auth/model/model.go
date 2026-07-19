package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Role/status values — mirror the CHECK constraints on auth_users exactly.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"

	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// User is one row of auth_users — the identity record. Password hash is
// deliberately NOT on this struct (it lives in auth_credentials and never
// leaves the repository except inside VerifyPassword).
type User struct {
	ID        uuid.UUID
	Email     string
	FullName  string
	Role      string
	Status    string
	KYCLevel  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type KYCSubmission struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	LevelRequested int
	Status         string
	Payload        map[string]any
	Provider       string
	ProviderRef    string
	DecidedBy      string
	DecisionReason string
	CreatedAt      time.Time
	DecidedAt      *time.Time
}

// KYCApplyRetry is the durable intent to re-apply ledger policy limits for a
// pending KYC approval.  It intentionally contains no ledger credentials or
// payload data; the submission is re-read by auth when the intent is claimed.
type KYCApplyRetry struct {
	ID             uuid.UUID
	SubmissionID   uuid.UUID
	UserID         uuid.UUID
	Level          int
	Direction      string
	DecidedBy      string
	DecisionReason string
	Status         string
	RetryCount     int
	NextAttemptAt  time.Time
	LastError      string
	LockedUntil    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// MarshalPayload keeps JSON encoding in the auth repository boundary.
func (s KYCSubmission) MarshalPayload() ([]byte, error) { return json.Marshal(s.Payload) }

// RefreshToken is one row of auth_refresh_tokens. TokenHash is the SHA-256
// hex of the opaque token — the raw token is never stored anywhere.
type RefreshToken struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	TokenHash  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	RevokedAt  *time.Time
	ReplacedBy *uuid.UUID
}
