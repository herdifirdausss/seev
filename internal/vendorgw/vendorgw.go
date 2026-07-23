// Package vendorgw is the vendor-adapter contract (docs/roadmap/archive/22 Task T1,
// decision K-T6): a normalized event/verifier shape that lets the payin
// module talk to any payment vendor without ever seeing that vendor's raw
// wire format. It is deliberately a library, not a service — internal/payin
// and internal/payout are its only intended callers (docs/roadmap/archive/21 topology
// map). It must never import internal/ledger or internal/payin: an adapter
// that could reach into either would defeat the point of the seam.
package vendorgw

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// PayinEvent is one normalized, verified payin webhook delivery — payin
// module code never sees a vendor's raw wire format, only this shape.
type PayinEvent struct {
	Vendor        string
	VendorEventID string // vendor's own event id — the dedup key
	ExternalRef   string // vendor's transaction ref — becomes ledger metadata external_ref
	UserID        uuid.UUID
	Amount        decimal.Decimal // minor units, integral
	Currency      string
	OccurredAt    time.Time
}

// ErrInvalidSignature is returned by PayinVerifier.VerifyAndParse when a
// delivery's signature doesn't match. Callers map this to HTTP 401 with no
// side effect (docs/roadmap/archive/22 Task T2 step 2) — never persisted, never
// retried automatically (a bad signature won't become valid on redelivery).
var ErrInvalidSignature = errors.New("vendorgw: invalid signature")

// ErrUnknownPayinVendor means no enabled payin verifier is registered.
// It lives in the shared vendor boundary so payin's gRPC transport can map
// it without importing the root payin facade and creating an import cycle.
var ErrUnknownPayinVendor = errors.New("vendorgw: unknown payin vendor")

// PayinVerifier verifies and parses one webhook delivery from a single
// vendor.
//
// VerifyAndParse MUST compute the signature over rawBody's raw bytes,
// never a JSON-decoded-then-re-marshaled form — a vendor signs exactly the
// bytes it sent over the wire, and decode-then-re-encode is not guaranteed
// byte-identical (map key order, escaping, whitespace all vary across
// encoders). Decode only AFTER the signature has verified against rawBody.
//
// Returns (nil, nil) when the signature is valid but the event isn't one
// payin cares about (docs/roadmap/archive/22 scope: settled events only) — the caller
// treats this as "acknowledged, ignored" (HTTP 200), not an error.
type PayinVerifier interface {
	// Vendor returns this verifier's registry name — must be stable and
	// match the name it's registered under (see Registry.AddPayin).
	Vendor() string
	VerifyAndParse(headers http.Header, rawBody []byte) (*PayinEvent, error)
}
