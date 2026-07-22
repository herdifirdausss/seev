package logger

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
)

const (
	maxDepth    = 50
	redacted    = "[REDACTED]"
	maxDepthStr = "[MAX_DEPTH]"
)

// sensitiveKeys adalah kunci JSON/form yang harus di-redact.
// Semua entry lowercase, tanpa separator.
var sensitiveKeys = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"accesstoken",
	"refreshtoken",
	"authorization",
	"apikey",
	"pin",
	"jwt",
	"signature",
	"credential",
	"privatekey",
	"clientsecret",
	// docs/plan/43 Task T6 / PROJECT_GUIDE.md: "do not expose ... full idempotency
	// keys in public logs" — Contains-based matching also catches
	// idempotency_key/idempotency_scope. "amount" is deliberately NOT
	// added here (see TestIsSensitiveKey's own "amount"->false case,
	// predates this task) — this masking layer scopes to
	// credentials/secrets, not general business data; the amount-in-logs
	// concern is handled precisely where it actually creates a
	// replay/correlation risk (a per-request structured logger pairing the
	// full idempotency key with its exact amount on every subsequent log
	// line — see internal/ledger/service/handle/service.go's Handle()),
	// not by redacting every amount field from every request/response body
	// system-wide.
	"idempotencykey",
}

var blockedHeaders = map[string]struct{}{
	"authorization":       {},
	"cookie":              {},
	"set-cookie":          {},
	"x-api-key":           {},
	"x-auth-token":        {},
	"proxy-authorization": {},
}

var sensitiveValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^bearer\s+\S+`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                                  // AWS access key
	regexp.MustCompile(`-----BEGIN\s+\S+\s+PRIVATE KEY-----`),               // PEM private key
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`), // JWT (3-part)
}

// normalizeKey menghapus semua separator dan mengubah ke lowercase
// sehingga "user_password", "user-password", "UserPassword" semuanya
// menjadi "userpassword" sebelum dibandingkan.
func normalizeKey(key string) string {
	k := strings.ToLower(key)
	k = strings.ReplaceAll(k, "-", "")
	k = strings.ReplaceAll(k, "_", "")
	k = strings.ReplaceAll(k, ".", "")
	return k
}

// isSensitiveKey mengembalikan true jika key mengandung salah satu
// kata sensitif setelah dinormalisasi.
//
// FIX: versi lama menggunakan HasPrefix/HasSuffix dengan "_" setelah
// strip, sehingga kondisi tersebut tidak pernah terpenuhi.
// Sekarang menggunakan strings.Contains yang benar.
func isSensitiveKey(key string) bool {
	k := normalizeKey(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// isSensitiveValue mengembalikan true jika nilai string cocok dengan
// salah satu pola credential yang dikenali.
func isSensitiveValue(v string) bool {
	for _, r := range sensitiveValuePatterns {
		if r.MatchString(v) {
			return true
		}
	}
	return false
}

// maskRecursive menelusuri payload secara rekursif dan meredact nilai
// yang sensitif baik berdasarkan key maupun value-nya.
//
// FIX: early-return pada tipe primitif selain string menghindari
// alokasi tidak perlu; hanya map/slice yang dialokasi ulang.
func maskRecursive(data any, depth int) any {
	if depth > maxDepth {
		return maxDepthStr
	}

	switch val := data.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, v := range val {
			if isSensitiveKey(k) {
				out[k] = redacted
				continue
			}
			out[k] = maskRecursive(v, depth+1)
		}
		return out

	case []any:
		arr := make([]any, len(val))
		for i, v := range val {
			arr[i] = maskRecursive(v, depth+1)
		}
		return arr

	case string:
		if isSensitiveValue(val) {
			return redacted
		}
		return val

	default:
		// int, float, bool, nil, json.Number — kembalikan apa adanya tanpa alokasi
		return val
	}
}

// maskPayload adalah entry point publik untuk masking payload JSON.
func maskPayload(v any) any {
	return maskRecursive(v, 0)
}

// parseMediaType mem-parse Content-Type header dan mengembalikan
// media type tanpa parameter (mis. "application/json").
func parseMediaType(ct string) string {
	if ct == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ""
	}
	return mt
}

// maxBodyRestoreBytes bounds how much of a request body this middleware
// will ever buffer into memory before restoring r.Body — a generous
// GLOBAL safety net, never the real per-route limit (docs/plan/49 TM-12).
// Must stay >= the largest legitimate body any WithLogger-wrapped route
// accepts, or that route's own real size limit (e.g. a handler's
// http.MaxBytesReader) would appear to reject an upload that was actually
// within ITS limit, because this middleware corrupted it first. Currently
// the largest known case is the ledger reconciliation CSV upload's own
// 10MiB cap (internal/ledger/transport/http.go maxReconCSVUploadBytes).
const maxBodyRestoreBytes = 10 << 20 // 10MiB

// readBody reads up to maxBodyRestoreBytes of the request body and
// restores r.Body with the FULL bytes read — a downstream handler must
// see exactly what the client sent. It returns a SEPARATE copy truncated
// to max bytes for the caller to log; that limit governs log-line size
// only and must never be the one used to reconstruct r.Body (docs/plan/49
// TM-12 — the previous version conflated the two, so any request body
// over the 16KiB log-line size was silently truncated before the real
// handler ever saw it, corrupting HMAC-signed payloads and multipart
// uploads well within their own documented, larger limits).
//
// FIX: jika Content-Encoding adalah gzip, body di-restore dengan bytes
// yang sudah didekompresi (bukan bytes terkompresi), dan LimitReader
// diterapkan pada dua level untuk mencegah gzip-bomb.
func readBody(r *http.Request, max int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyRestoreBytes+1))
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return nil, err
	}

	if !strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return truncateForLog(raw, max), nil
	}

	// Dekompresi gzip
	gr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		// Gagal buka gzip — kembalikan raw agar body tetap terbaca
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return nil, err
	}
	defer gr.Close()

	decompressed, err := io.ReadAll(io.LimitReader(gr, maxBodyRestoreBytes))
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return nil, err
	}

	// FIX: restore dengan bytes dekompresi agar handler downstream
	// menerima payload yang sudah siap pakai
	r.Body = io.NopCloser(bytes.NewReader(decompressed))
	return truncateForLog(decompressed, max), nil
}

// truncateForLog caps body at max bytes for LOG-LINE purposes only —
// never used to decide what a downstream handler receives (see readBody).
func truncateForLog(body []byte, max int64) []byte {
	if int64(len(body)) > max {
		return body[:max]
	}
	return body
}

// decodeJSON mem-parse JSON menggunakan json.Number agar presisi
// angka besar (int64/float64) tidak hilang saat di-marshal kembali.
func decodeJSON(body []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var payload any
	if err := dec.Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// ReadAndMaskRequestBody membaca body request, mem-parse jika JSON,
// lalu meredact semua field sensitif. Body di-restore agar bisa
// dibaca kembali oleh handler downstream.
func ReadAndMaskRequestBody(r *http.Request, max int64) any {
	body, err := readBody(r, max)
	if err != nil || len(body) == 0 {
		return nil
	}

	mt := parseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return map[string]any{
			"type":   "non-json-request",
			"length": len(body),
			"mime":   mt,
		}
	}

	payload, err := decodeJSON(body)
	if err != nil {
		return "[INVALID_JSON]"
	}

	return maskPayload(payload)
}

// MaskResponseBody mem-parse body response JSON dan meredact field
// sensitif. Untuk non-JSON, mengembalikan metadata ringkas.
func MaskResponseBody(body []byte, contentType string) any {
	mt := parseMediaType(contentType)

	if mt != "application/json" {
		return map[string]any{
			"type":   "non-json-response",
			"length": len(body),
			"mime":   mt,
		}
	}

	payload, err := decodeJSON(body)
	if err != nil {
		return "[INVALID_JSON_RESPONSE]"
	}

	return maskPayload(payload)
}

// SanitizeHeaders mengembalikan copy header yang sudah disanitasi:
// header yang diblokir diganti "[REDACTED]", dan nilai header lain
// dicek terhadap sensitive value pattern.
//
// FIX: tambahkan pengecekan isSensitiveValue agar header seperti
// X-Custom-Token: Bearer abc123 juga ikut di-redact.
func SanitizeHeaders(headers http.Header) map[string]string {
	out := make(map[string]string, len(headers))

	for k, vals := range headers {
		lk := strings.ToLower(k)
		canonical := http.CanonicalHeaderKey(k)

		if _, blocked := blockedHeaders[lk]; blocked {
			out[canonical] = redacted
			continue
		}

		joined := strings.Join(vals, ",")
		if isSensitiveValue(joined) {
			out[canonical] = redacted
			continue
		}

		out[canonical] = joined
	}

	return out
}
