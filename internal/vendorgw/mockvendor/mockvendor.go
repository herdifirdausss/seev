// Package mockvendor is a stand-in payment vendor (docs/plan/22 Task T1,
// decision K-T6) — used until a real vendor account exists. Its webhook
// shape and HMAC-SHA256 signature scheme are made up (not modeled on any
// real vendor); the point is to exercise internal/vendorgw's contract and
// internal/payin's dedup/posting logic end-to-end. A real vendor is added
// later as a sibling subpackage — internal/payin never changes.
package mockvendor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/vendorgw"
)

// VendorName is this verifier's registry name.
const VendorName = "mockvendor"

// settledEventType is the only webhookPayload.Type VerifyAndParse turns
// into a PayinEvent (docs/plan/22 scope: settled-only) — anything else is
// acknowledged but ignored.
const settledEventType = "payment.settled"

// SignatureHeader is where mockvendor expects its HMAC signature.
const SignatureHeader = "X-Mock-Signature"

// Verifier implements vendorgw.PayinVerifier for mockvendor.
type Verifier struct {
	secret string
}

func New(secret string) *Verifier {
	return &Verifier{secret: secret}
}

func (v *Verifier) Vendor() string { return VendorName }

// webhookPayload is mockvendor's made-up wire format. Amount is a string,
// not a JSON number — a JSON number decodes to float64, which is exactly
// the float-money bug this whole codebase's decimal.Decimal discipline
// exists to prevent; a real vendor's integer-minor-unit field would decode
// natively, but mockvendor deliberately mimics the string-amount pattern
// several real vendors actually use.
type webhookPayload struct {
	EventID     string    `json:"event_id"`
	ExternalRef string    `json:"external_ref"`
	UserID      string    `json:"user_id"`
	Amount      string    `json:"amount"`
	Currency    string    `json:"currency"`
	OccurredAt  time.Time `json:"occurred_at"`
	Type        string    `json:"type"`
}

func (v *Verifier) VerifyAndParse(headers http.Header, rawBody []byte) (*vendorgw.PayinEvent, error) {
	sig := headers.Get(SignatureHeader)
	if sig == "" {
		return nil, vendorgw.ErrInvalidSignature
	}
	expected := Sign(v.secret, rawBody)
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return nil, vendorgw.ErrInvalidSignature
	}

	// Decode ONLY after the signature verified against the raw bytes —
	// never decode-then-verify (see PayinVerifier's doc comment).
	var payload webhookPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return nil, fmt.Errorf("mockvendor: parse payload: %w", err)
	}
	if payload.Type != settledEventType {
		return nil, nil
	}
	if payload.EventID == "" || payload.ExternalRef == "" {
		return nil, errors.New("mockvendor: missing event_id or external_ref")
	}

	amount, err := decimal.NewFromString(payload.Amount)
	if err != nil {
		return nil, fmt.Errorf("mockvendor: parse amount: %w", err)
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return nil, fmt.Errorf("mockvendor: parse user_id: %w", err)
	}

	return &vendorgw.PayinEvent{
		Vendor:        VendorName,
		VendorEventID: payload.EventID,
		ExternalRef:   payload.ExternalRef,
		UserID:        userID,
		Amount:        amount,
		Currency:      payload.Currency,
		OccurredAt:    payload.OccurredAt,
	}, nil
}

// Sign computes the HMAC-SHA256 signature mockvendor expects in the
// X-Mock-Signature header, hex-encoded. Exported for tests and for
// producing a validly-signed request when smoke-testing the webhook
// receiver by hand.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
