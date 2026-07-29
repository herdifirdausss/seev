package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignatureHeader is the HTTP header name every delivery carries — the
// LOCKED scheme from docs/reference/c1-b2b-design.md §2:
// "Seev-Signature: t=<unix ts>,v1=<hex hmac-sha256>".
const SignatureHeader = "Seev-Signature"

// Sign computes v1 = HMAC-SHA256(secret, "{t}.{body}") and returns the
// full header value. t is NOT refreshed on retry (design doc: "Retries
// resend the exact same bytes and id; the timestamp is NOT refreshed on
// retry, so v1 stays reproducible for a given attempt log") — callers
// must pass the SAME t for every attempt of one delivery, computed once
// when the delivery is first created, never per-attempt.
func Sign(secret []byte, t int64, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strconv.FormatInt(t, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	sum := mac.Sum(nil)
	return fmt.Sprintf("t=%d,v1=%s", t, hex.EncodeToString(sum))
}

// Verify recomputes the signature and reports whether header matches body
// under secret — published as a reference implementation for receiver
// verification documentation/examples (T7's own "Publish receiver
// verification documentation and examples" work item) and reused by this
// package's own tests. Uses hmac.Equal for constant-time comparison, same
// posture as every other secret comparison in this codebase (T3's API key
// digest, K9's rotation drill).
func Verify(secret []byte, header string, body []byte) bool {
	t, v1, ok := parseSignatureHeader(header)
	if !ok {
		return false
	}
	want := Sign(secret, t, body)
	_, wantV1, _ := parseSignatureHeader(want)
	return hmac.Equal([]byte(v1), []byte(wantV1))
}

// VerifyWithTolerance is Verify plus a bound on how old the signed
// timestamp may be — the receiver-side replay-window check every
// verification example in the published docs recommends (5 minutes is
// this codebase's own established default elsewhere, e.g. TM-08's
// webhook timestamp discussion).
func VerifyWithTolerance(secret []byte, header string, body []byte, now time.Time, tolerance time.Duration) bool {
	t, _, ok := parseSignatureHeader(header)
	if !ok {
		return false
	}
	signedAt := time.Unix(t, 0)
	if signedAt.Before(now.Add(-tolerance)) || signedAt.After(now.Add(tolerance)) {
		return false
	}
	return Verify(secret, header, body)
}

func parseSignatureHeader(header string) (t int64, v1 string, ok bool) {
	parts := strings.Split(header, ",")
	if len(parts) != 2 {
		return 0, "", false
	}
	for _, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			return 0, "", false
		}
		switch kv[0] {
		case "t":
			parsed, err := strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				return 0, "", false
			}
			t = parsed
		case "v1":
			v1 = kv[1]
		default:
			return 0, "", false
		}
	}
	if t == 0 || v1 == "" {
		return 0, "", false
	}
	return t, v1, true
}
