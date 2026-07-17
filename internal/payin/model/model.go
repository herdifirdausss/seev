package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// WebhookEvent is one row of payin_webhook_events (docs/plan/22 Task T2) —
// one vendor webhook delivery, deduped by (Vendor, VendorEventID).
type WebhookEvent struct {
	ID            uuid.UUID
	Vendor        string
	VendorEventID string
	ExternalRef   string
	UserID        uuid.UUID
	Amount        decimal.Decimal
	Currency      string
	Raw           []byte // raw webhook body, forensic/replay — never exposed in any reporting view
	Status        string // received | posted | failed
	ErrorMessage  string
	// RequestID (docs/plan/36 Task T5) is the HTTP request_id of the gateway
	// call that received this webhook delivery — end-to-end trace anchor.
	RequestID string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Topup intent status values (docs/plan/25 Task T3).
const (
	TopupStatusPending = "pending"
	TopupStatusSettled = "settled"
	TopupStatusExpired = "expired"
)

// TopupIntent is one row of payin_topup_intents (docs/plan/25 Task T3) — a
// user-initiated top-up request. Reference is what the user quotes at the
// vendor, carried back in the settling webhook's existing ExternalRef
// field (zero vendorgw/mockvendor schema change).
type TopupIntent struct {
	ID             uuid.UUID
	Reference      string
	UserID         uuid.UUID
	Amount         decimal.Decimal
	Currency       string
	Vendor         string
	Status         string // pending | settled | expired
	SettledEventID *uuid.UUID
	ExpiresAt      time.Time
	// RequestID (docs/plan/36 Task T5) is the HTTP request_id of the call
	// that created this intent — end-to-end trace anchor.
	RequestID string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// RoutingRule selects a vendor for a flow. Nil match fields are wildcards.
type RoutingRule struct {
	ID        uuid.UUID  `json:"id"`
	Flow      string     `json:"flow"`
	Priority  int        `json:"priority"`
	Enabled   bool       `json:"enabled"`
	Currency  *string    `json:"currency,omitempty"`
	MinAmount *int64     `json:"min_amount,omitempty"`
	MaxAmount *int64     `json:"max_amount,omitempty"`
	UserID    *uuid.UUID `json:"user_id,omitempty"`
	Vendor    string     `json:"vendor"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type VendorGateway struct {
	Vendor  string `json:"vendor"`
	Gateway string `json:"gateway"`
}

// RoutingCandidate is one matching routing rule's vendor+gateway, part of
// the ordered candidate list ResolveCandidates returns (docs/plan/40 Task
// T2) — replaces the old single-winner Resolve so the caller can skip a
// candidate whose circuit is open and fall through to the next.
type RoutingCandidate struct {
	Vendor  string
	Gateway string
}
