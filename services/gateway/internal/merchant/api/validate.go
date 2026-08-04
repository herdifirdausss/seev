package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"

	"github.com/shopspring/decimal"
)

// maxRequestBodyBytes bounds every B2B request body — same 1 MiB ceiling
// internal/platform/transport/http/response.Decode already uses elsewhere in this codebase.
const maxRequestBodyBytes = 1 << 20

// currencyPattern mirrors contracts/http/components/common.yaml's Currency
// schema exactly (`^[A-Z]{3}$`).
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// readJSONBody reads the bounded raw request body once and decodes it into
// dst, rejecting unknown fields (contracts/http/b2b-v1.yaml's
// CreatePayinRequest/CreatePayoutRequest both set additionalProperties:
// false). The raw bytes are returned too — T4's idempotency hash
// (idempotency.CanonicalRequestHash) must be computed over the EXACT
// bytes the caller sent, not a re-marshaled struct.
func readJSONBody(w http.ResponseWriter, r *http.Request, dst any) (raw []byte, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeValidationError(w, "request body too large or unreadable")
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeValidationError(w, "invalid request body")
		return nil, false
	}
	return body, true
}

// validateAmount enforces §3.5's exact-money rule at the Gateway edge: a
// positive integer decimal string, matching contracts/http/components/common.yaml's
// MoneyMinor pattern (`^[0-9]+$`) plus the "positive" requirement every
// owner-service create path already enforces
// (services/payin/internal/transport/grpc.parseUserAndAmount and its payout twin) — a
// zero or negative amount fails at the edge rather than round-tripping to
// the owner service first.
func validateAmount(raw string) (decimal.Decimal, bool) {
	amount, err := decimal.NewFromString(raw)
	if err != nil || !amount.Equal(amount.Truncate(0)) || !amount.IsPositive() {
		return decimal.Decimal{}, false
	}
	return amount, true
}

// validateCurrency enforces contracts/http/components/common.yaml's Currency
// schema (`^[A-Z]{3}$`) at the Gateway edge.
func validateCurrency(raw string) bool {
	return currencyPattern.MatchString(raw)
}
