package mockvendor

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/vendorgw"
)

const testSecret = "test-secret"

func signedRequest(t *testing.T, secret string, body []byte) http.Header {
	t.Helper()
	h := http.Header{}
	h.Set(SignatureHeader, Sign(secret, body))
	return h
}

func validSettledBody(userID uuid.UUID) []byte {
	return []byte(`{"event_id":"evt-1","external_ref":"ref-1","user_id":"` + userID.String() +
		`","amount":"100000","currency":"IDR","occurred_at":"2026-07-13T00:00:00Z","type":"payment.settled"}`)
}

func TestVerifyAndParse_ValidSignature_ReturnsNormalizedEvent(t *testing.T) {
	userID := uuid.New()
	body := validSettledBody(userID)
	headers := signedRequest(t, testSecret, body)

	v := New(VendorName, testSecret)
	ev, err := v.VerifyAndParse(headers, body)
	require.NoError(t, err)
	require.NotNil(t, ev)

	assert.Equal(t, "mockvendor", ev.Vendor)
	assert.Equal(t, "evt-1", ev.VendorEventID)
	assert.Equal(t, "ref-1", ev.ExternalRef)
	assert.Equal(t, userID, ev.UserID)
	assert.True(t, ev.Amount.Equal(decimal.NewFromInt(100_000)), "amount must be exact decimal, not float — got %s", ev.Amount)
	assert.Equal(t, "IDR", ev.Currency)
	assert.False(t, ev.OccurredAt.IsZero())
}

func TestVerifyAndParse_MissingSignatureHeader_ErrInvalidSignature(t *testing.T) {
	body := validSettledBody(uuid.New())
	v := New(VendorName, testSecret)
	ev, err := v.VerifyAndParse(http.Header{}, body)
	assert.Nil(t, ev)
	assert.True(t, errors.Is(err, vendorgw.ErrInvalidSignature))
}

func TestVerifyAndParse_WrongSecret_ErrInvalidSignature(t *testing.T) {
	body := validSettledBody(uuid.New())
	headers := signedRequest(t, "wrong-secret", body)

	v := New(VendorName, testSecret)
	ev, err := v.VerifyAndParse(headers, body)
	assert.Nil(t, ev)
	assert.True(t, errors.Is(err, vendorgw.ErrInvalidSignature))
}

func TestVerifyAndParse_BodyTamperedAfterSigning_ErrInvalidSignature(t *testing.T) {
	body := validSettledBody(uuid.New())
	headers := signedRequest(t, testSecret, body)

	tampered := make([]byte, len(body))
	copy(tampered, body)
	tampered[len(tampered)-2] = 'X' // flip one byte inside the JSON, signature no longer matches

	v := New(VendorName, testSecret)
	ev, err := v.VerifyAndParse(headers, tampered)
	assert.Nil(t, ev)
	assert.True(t, errors.Is(err, vendorgw.ErrInvalidSignature))
}

func TestVerifyAndParse_NonSettledType_ReturnsNilNil(t *testing.T) {
	body := []byte(`{"event_id":"evt-2","external_ref":"ref-2","user_id":"` + uuid.New().String() +
		`","amount":"1000","currency":"IDR","occurred_at":"2026-07-13T00:00:00Z","type":"payment.pending"}`)
	headers := signedRequest(t, testSecret, body)

	v := New(VendorName, testSecret)
	ev, err := v.VerifyAndParse(headers, body)
	assert.Nil(t, ev)
	assert.NoError(t, err, "a valid signature over a non-settled event must be acknowledged, not errored")
}

// TestVerifyAndParse_SignatureIsOverRawBytes_NotReMarshaledJSON proves the
// DoD requirement (docs/plan/22 Task T1): reordering the JSON object's keys
// changes the raw bytes (and therefore what a naive "decode then
// re-marshal then check" implementation would sign), but the signature
// here is computed and verified against the ORIGINAL bytes the "vendor"
// sent — so a signature computed over one key order must fail verification
// against a body with a different key order, proving VerifyAndParse never
// re-marshals before checking.
func TestVerifyAndParse_SignatureIsOverRawBytes_NotReMarshaledJSON(t *testing.T) {
	userID := uuid.New()
	original := validSettledBody(userID)
	reordered := []byte(`{"type":"payment.settled","currency":"IDR","amount":"100000","user_id":"` + userID.String() +
		`","external_ref":"ref-1","event_id":"evt-1","occurred_at":"2026-07-13T00:00:00Z"}`)
	require.NotEqual(t, string(original), string(reordered), "test fixture must actually differ byte-for-byte")

	sigForOriginal := Sign(testSecret, original)
	headers := http.Header{}
	headers.Set(SignatureHeader, sigForOriginal)

	v := New(VendorName, testSecret)
	// Verifying the ORIGINAL bytes against their own signature must pass.
	ev, err := v.VerifyAndParse(headers, original)
	require.NoError(t, err)
	require.NotNil(t, ev)

	// The SAME signature checked against the reordered (semantically
	// identical, byte-different) body must fail — proves the check is
	// against rawBody, not a decoded-and-re-encoded form.
	ev2, err2 := v.VerifyAndParse(headers, reordered)
	assert.Nil(t, ev2)
	assert.True(t, errors.Is(err2, vendorgw.ErrInvalidSignature))
}

func TestVerifyAndParse_MissingEventIDOrExternalRef_Error(t *testing.T) {
	body := []byte(`{"event_id":"","external_ref":"ref-1","user_id":"` + uuid.New().String() +
		`","amount":"1000","currency":"IDR","occurred_at":"2026-07-13T00:00:00Z","type":"payment.settled"}`)
	headers := signedRequest(t, testSecret, body)

	v := New(VendorName, testSecret)
	ev, err := v.VerifyAndParse(headers, body)
	assert.Nil(t, ev)
	assert.Error(t, err)
	assert.False(t, errors.Is(err, vendorgw.ErrInvalidSignature), "this is a payload error, not a signature error")
}

func TestVerifyAndParse_InvalidAmount_Error(t *testing.T) {
	body := []byte(`{"event_id":"evt-3","external_ref":"ref-3","user_id":"` + uuid.New().String() +
		`","amount":"not-a-number","currency":"IDR","occurred_at":"2026-07-13T00:00:00Z","type":"payment.settled"}`)
	headers := signedRequest(t, testSecret, body)

	v := New(VendorName, testSecret)
	ev, err := v.VerifyAndParse(headers, body)
	assert.Nil(t, ev)
	assert.Error(t, err)
}

func TestVendor_ReturnsRegistryName(t *testing.T) {
	v := New(VendorName, testSecret)
	assert.Equal(t, "mockvendor", v.Vendor())
}

// TestVerifyAndParse_SecondNamedInstance_TagsEventWithOwnNameAndSecret is
// docs/plan/40 Task T4's required test: a second named Verifier instance
// (e.g. "mockvendor2", registered alongside "mockvendor" in the same
// Registry) must be fully isolated — its own secret verifies its own
// signatures, the OTHER instance's secret must NOT verify them, and the
// resulting PayinEvent.Vendor must carry ITS OWN name (this event field
// feeds both the idempotency scope and the vendor-gateway routing lookup
// in internal/payin, so a wrong/hardcoded name would misattribute
// mockvendor2 traffic to mockvendor throughout the rest of the pipeline).
func TestVerifyAndParse_SecondNamedInstance_TagsEventWithOwnNameAndSecret(t *testing.T) {
	const secret2 = "second-vendor-secret"
	v2 := New("mockvendor2", secret2)

	userID := uuid.New()
	body := validSettledBody(userID)

	ev, err := v2.VerifyAndParse(signedRequest(t, secret2, body), body)
	require.NoError(t, err)
	require.NotNil(t, ev)
	assert.Equal(t, "mockvendor2", ev.Vendor, "the event must be tagged with THIS instance's own name, not the package-level VendorName constant")

	_, err = v2.VerifyAndParse(signedRequest(t, testSecret, body), body)
	assert.ErrorIs(t, err, vendorgw.ErrInvalidSignature, "mockvendor's secret must not verify mockvendor2's signature")

	v1 := New(VendorName, testSecret)
	_, err = v1.VerifyAndParse(signedRequest(t, secret2, body), body)
	assert.ErrorIs(t, err, vendorgw.ErrInvalidSignature, "mockvendor2's secret must not verify mockvendor's signature")
}

func TestSign_Deterministic(t *testing.T) {
	body := []byte("hello")
	assert.Equal(t, Sign(testSecret, body), Sign(testSecret, body))
	assert.NotEqual(t, Sign(testSecret, body), Sign("other-secret", body))
}
