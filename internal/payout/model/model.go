package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Status values for PayoutRequest.Status (docs/plan/23 Task T1) — mirrors
// the CHECK constraint on payout_requests.status exactly.
const (
	StatusCreated       = "created"
	StatusHeld          = "held"
	StatusSubmitted     = "submitted"
	StatusVendorPending = "vendor_pending"
	StatusSettled       = "settled"
	StatusFailed        = "failed"
	StatusCancelled     = "cancelled"
	// StatusRejected (docs/plan/38 Task T5) is terminal, reached ONLY when
	// a fee quote consumption fails (expired/mismatch) at Create — no hold
	// was ever posted; the row exists purely to record the rejected
	// attempt, distinct from StatusFailed (a submit/vendor-call failure
	// after a hold already moved money into the hold account).
	StatusRejected = "rejected"
)

// Vendor call outcome values for PayoutVendorCall.Outcome (docs/plan/40
// Task T3) — the classification that drives the anti-double-payout
// failover rule (mayFailover): failover is DIYIZINKAN only while NO call
// for a request has ever landed accepted or uncertain.
const (
	// VendorCallAccepted: the vendor took the request (Settled or Pending)
	// — reachable/synchronous, pinned forward toward completion.
	VendorCallAccepted = "accepted"
	// VendorCallRejected: a definitive SYNCHRONOUS business rejection (the
	// vendor was reachable and said no) — failover to another candidate is
	// allowed, and this does NOT trip the circuit breaker (gotcha #13
	// master: business rejections are not infra failures).
	VendorCallRejected = "rejected"
	// VendorCallUncertain: a transport/infra error (timeout, 5xx, unknown)
	// — the vendor may or may not have received/actioned the request, so
	// the payout is PINNED to this vendor forever (recovery = Query/retry
	// the SAME vendor via the resume job, never failover).
	VendorCallUncertain = "uncertain"
)

// PayoutRequest is one row of payout_requests.
type PayoutRequest struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	Amount       decimal.Decimal
	Currency     string
	Vendor       string
	Destination  []byte // vendor-shaped JSON, e.g. {"bank_code":"...","account_no":"..."}
	Status       string
	HoldTxID     *uuid.UUID
	SettleTxID   *uuid.UUID
	VendorRef    string
	ErrorMessage string
	CreatedBy    string
	// RequestID (docs/plan/36 Task T5) is the HTTP request_id of the call
	// that created this payout — end-to-end trace anchor. NOT the same
	// concept as PayoutVendorCall.PayoutRequestID below (that one is this
	// row's own id, referenced as a foreign key).
	RequestID string
	// FeeQuoteID/FeeAmount/FeeGateway (docs/plan/38 Task T5) are set ONLY
	// when Create consumed a fee quote — nil/zero otherwise, in which case
	// settle falls back to ResolveFee exactly as before this feature
	// existed. FeeAmount is a pointer (not decimal.Decimal's own zero
	// value) so "no quote used" (NULL) is distinguishable from "quote
	// locked in a zero fee" (0) — settle branches on FeeQuoteID being nil,
	// not on FeeAmount being zero, but the pointer keeps the DB column's
	// NULL-ness faithfully represented in Go too.
	FeeQuoteID *uuid.UUID
	FeeAmount  *decimal.Decimal
	FeeGateway string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// PayoutVendorCall is one row of payout_vendor_calls — one outbound attempt.
type PayoutVendorCall struct {
	ID uuid.UUID
	// PayoutRequestID is the payout_requests.id this call belongs to (FK) —
	// renamed from "RequestID" (docs/plan/36 Task T5) to stop colliding in
	// name with the HTTP/gRPC trace request_id, which this is NOT.
	PayoutRequestID uuid.UUID
	Attempt         int
	ReqSummary      string
	RespStatus      string
	Error           string
	// Outcome classifies this call (docs/plan/40 Task T3) — one of
	// VendorCallAccepted/Rejected/Uncertain. Drives the anti-double-payout
	// failover rule (mayFailover): the source of truth is THIS column
	// across every call ever recorded for a request, never breaker state.
	Outcome   string
	CreatedAt time.Time
}

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
// candidate whose circuit is open or who is in an exclusion list (already
// tried this request) and fall through to the next.
type RoutingCandidate struct {
	Vendor  string
	Gateway string
}
