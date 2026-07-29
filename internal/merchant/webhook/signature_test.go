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

// FuzzParseSignatureHeader fuzzes the untrusted-input entry point every
// published receiver-verification example (docs/reference/webhook-receiver-guide.md)
// tells a MERCHANT to implement themselves against a header this service
// controls — proving Seev's own reference implementation never panics on a
// malformed "Seev-Signature" value is exactly what T10's fuzz requirement
// (§23.2 "signature header parser") is protecting.
func FuzzParseSignatureHeader(f *testing.F) {
	seeds := []string{
		"t=1735808645,v1=abc123",
		"",
		"t=1735808645",
		"v1=abc123",
		"t=notanumber,v1=abc123",
		"t=1735808645,v1=",
		"t=,v1=abc123",
		"t=1735808645,v1=abc123,extra=1",
		"t=1735808645;v1=abc123",
		"a=b,c=d",
		",",
		"t=99999999999999999999999999,v1=abc123",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, header string) {
		// Must never panic; ok=false is always an acceptable outcome for
		// malformed input, but a true result must carry a non-zero
		// timestamp and non-empty v1 (parseSignatureHeader's own
		// documented postcondition).
		ts, v1, ok := parseSignatureHeader(header)
		if ok && (ts == 0 || v1 == "") {
			t.Fatalf("parseSignatureHeader(%q) returned ok=true with an invalid zero-value result (t=%d, v1=%q)", header, ts, v1)
		}
	})
}
