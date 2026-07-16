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

// readBody membaca body request hingga max byte, lalu me-restore body
// agar bisa dibaca ulang oleh handler downstream.
//
// FIX: jika Content-Encoding adalah gzip, body di-restore dengan bytes
// yang sudah didekompresi (bukan bytes terkompresi), dan LimitReader
// diterapkan pada dua level untuk mencegah gzip-bomb.
func readBody(r *http.Request, max int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return nil, err
	}

	// Potong tepat di batas max agar tidak bocor lebih dari yang diminta
	if int64(len(raw)) > max {
		raw = raw[:max]
	}

	if !strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return raw, nil
	}

	// Dekompresi gzip
	gr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		// Gagal buka gzip — kembalikan raw agar body tetap terbaca
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return nil, err
	}
	defer gr.Close()

	decompressed, err := io.ReadAll(io.LimitReader(gr, max))
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return nil, err
	}

	// FIX: restore dengan bytes dekompresi agar handler downstream
	// menerima payload yang sudah siap pakai
	r.Body = io.NopCloser(bytes.NewReader(decompressed))
	return decompressed, nil
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
