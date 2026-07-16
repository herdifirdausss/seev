package vendorgw

import (
	"context"
	"encoding/json"

	"github.com/shopspring/decimal"
)

// PayoutStatus is a vendor's reported outcome for one payout submission.
type PayoutStatus string

const (
	PayoutSettled PayoutStatus = "settled"
	PayoutPending PayoutStatus = "pending"
	PayoutFailed  PayoutStatus = "failed"
)

// PayoutResult is a vendor's normalized response to Submit or Query.
type PayoutResult struct {
	VendorRef string
	Status    PayoutStatus
	Reason    string // set when Status == PayoutFailed
}

// PayoutProvider submits and polls one vendor's payout (withdrawal) API.
type PayoutProvider interface {
	Vendor() string

	// Submit initiates a payout. idempotencyKey (= payout_requests.id,
	// docs/plan/23 Task T1) MUST make a repeated Submit with the same key
	// safe — the vendor (or this provider, standing in for it) must never
	// send money twice for the same key, and must return the SAME result
	// both times.
	Submit(ctx context.Context, idempotencyKey string, amount decimal.Decimal, currency string, destination json.RawMessage) (PayoutResult, error)

	// Query polls the current status of a previously-submitted payout —
	// used by the resume/polling job (docs/plan/23 Task T3) for requests
	// still Pending.
	Query(ctx context.Context, idempotencyKey string) (PayoutResult, error)
}
