package webhook

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSign_Deterministic(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte(`{"id":"evt_1","type":"transaction.posted.v1"}`)

	a := Sign(secret, 1735808645, body)
	b := Sign(secret, 1735808645, body)
	assert.Equal(t, a, b, "same secret/t/body must always produce the same signature — retries reuse the exact bytes")
}

func TestSign_MatchesLockedFormat(t *testing.T) {
	// docs/reference/c1-b2b-design.md §2: "Seev-Signature: t=<unix
	// ts>,v1=<hex hmac-sha256>".
	sig := Sign([]byte("secret"), 1735808645, []byte("body"))
	assert.Regexp(t, `^t=1735808645,v1=[0-9a-f]{64}$`, sig)
}

func TestVerify_RoundTrip(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte(`{"id":"evt_1"}`)
	sig := Sign(secret, time.Now().Unix(), body)
	assert.True(t, Verify(secret, sig, body))
}

func TestVerify_WrongSecretFails(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	sig := Sign([]byte("secret-a"), 1735808645, body)
	assert.False(t, Verify([]byte("secret-b"), sig, body))
}

func TestVerify_TamperedBodyFails(t *testing.T) {
	secret := []byte("test-secret")
	sig := Sign(secret, 1735808645, []byte(`{"amount":"100"}`))
	assert.False(t, Verify(secret, sig, []byte(`{"amount":"999999"}`)))
}

func TestVerify_MalformedHeaderFails(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte("body")
	cases := []string{
		"",
		"garbage",
		"t=abc,v1=deadbeef",
		"v1=deadbeef",
		"t=123",
		"t=123,v1=deadbeef,extra=1",
	}
	for _, header := range cases {
		assert.False(t, Verify(secret, header, body), "header %q must not verify", header)
	}
}

func TestVerifyWithTolerance_WithinWindow(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte("body")
	now := time.Now()
	sig := Sign(secret, now.Add(-2*time.Minute).Unix(), body)
	assert.True(t, VerifyWithTolerance(secret, sig, body, now, 5*time.Minute))
}

func TestVerifyWithTolerance_OutsideWindowRejected(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte("body")
	now := time.Now()
	sig := Sign(secret, now.Add(-10*time.Minute).Unix(), body)
	assert.False(t, VerifyWithTolerance(secret, sig, body, now, 5*time.Minute),
		"a signature older than the tolerance window must be rejected — the receiver-side replay-window check")
}

func TestVerifyWithTolerance_FutureTimestampRejected(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte("body")
	now := time.Now()
	sig := Sign(secret, now.Add(10*time.Minute).Unix(), body)
	assert.False(t, VerifyWithTolerance(secret, sig, body, now, 5*time.Minute))
}

func TestSign_TimestampNotRefreshedAcrossRetries(t *testing.T) {
	// design doc: "the timestamp is NOT refreshed on retry, so v1 stays
	// reproducible for a given attempt log" — simulate 3 delivery attempts
	// of the SAME logical delivery, all using the delivery's own fixed t.
	secret := []byte("test-secret")
	body := []byte(`{"id":"evt_1"}`)
	fixedT := time.Now().Add(-1 * time.Minute).Unix()

	var sigs []string
	for range 3 {
		sigs = append(sigs, Sign(secret, fixedT, body))
	}
	require.Len(t, sigs, 3)
	assert.Equal(t, sigs[0], sigs[1])
	assert.Equal(t, sigs[1], sigs[2])
}
