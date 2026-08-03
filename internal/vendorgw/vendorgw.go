// Package vendorgw is the vendor-adapter contract (docs/roadmap/archive/22 Task T1,
// decision K-T6): a normalized event/verifier shape that lets the payin
// module talk to any payment vendor without ever seeing that vendor's raw
// wire format. It is deliberately a library, not a service — VendorService
// owns callback verification and outbound adapter composition, while Payin
// and Payout consume only the routing/provider contracts. It must never import
// internal/ledger or internal/payin: an adapter
// that could reach into either would defeat the point of the seam.
package vendorgw

import (
	"errors"
	"net/http"
	"time"

	"github.com/shopspring/decimal"
)

// PayinEvent is the vendor adapter's verified data before VendorService maps
// it to its owner-neutral callback contract. It intentionally contains no
// Seev user identity.
type PayinEvent struct {
	Vendor        string
	VendorEventID string          // vendor's own event id — the dedup key
	ExternalRef   string          // vendor's transaction ref — becomes ledger metadata external_ref
	Amount        decimal.Decimal // minor units, integral
	Currency      string
	OccurredAt    time.Time
}

// ErrInvalidSignature is returned by PayinVerifier.VerifyAndParse when a
// VendorService delivery's signature doesn't match. VendorService maps this
// to HTTP 401 with no side effect — never persisted, never retried
// automatically (a bad signature won't become valid on redelivery).
var ErrInvalidSignature = errors.New("vendorgw: invalid signature")

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

// PayinVendor is the routing/catalogue contract used by Payin. Callback
// verification is intentionally not part of it: raw vendor callbacks belong
// to VendorService and are delivered to Payin only after normalization.
type PayinVendor interface {
	Vendor() string
}

// CurrencyCapability is an additive vendor declaration used by journey
// routing. Older adapters may omit it and remain compatible with the legacy
// IDR route; every non-IDR route must explicitly declare the requested
// operation/currency pair.
type CurrencyCapability interface {
	SupportsCurrency(operation, currency string) bool
}

// SupportsRequestedCurrency preserves the legacy IDR adapter contract while
// making non-IDR capability declarations mandatory. A route must never infer
// USD support merely because an adapter happens to implement the older
// operation interface.
func SupportsRequestedCurrency(provider any, operation, currency string) bool {
	if currency == "" || currency == "IDR" {
		return true
	}
	capability, ok := provider.(CurrencyCapability)
	return ok && capability.SupportsCurrency(operation, currency)
}
