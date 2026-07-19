package logger

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────
// Helper
// ─────────────────────────────────────────────────────────────

func makeRequest(body string, contentType string, contentEncoding string) *http.Request {
	var bodyReader io.Reader = strings.NewReader(body)

	if strings.EqualFold(contentEncoding, "gzip") {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		_, _ = gw.Write([]byte(body))
		_ = gw.Close()
		bodyReader = &buf
	}

	req, _ := http.NewRequest(http.MethodPost, "/", bodyReader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if contentEncoding != "" {
		req.Header.Set("Content-Encoding", contentEncoding)
	}
	return req
}

// ─────────────────────────────────────────────────────────────
// isSensitiveKey
// ─────────────────────────────────────────────────────────────

func TestIsSensitiveKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		// Exact matches setelah normalisasi
		{"password", true},
		{"PASSWORD", true},
		{"token", true},
		{"jwt", true},
		{"pin", true},

		// Dengan separator — ini adalah bug di versi lama yang sudah diperbaiki
		{"user_password", true},
		{"user-password", true},
		{"access_token", true},
		{"refresh-token", true},
		{"api_key", true},
		{"API_KEY", true},
		{"client_secret", true},

		// CamelCase
		{"userPassword", true},
		{"accessToken", true},

		// Tidak sensitif
		{"username", false},
		{"email", false},
		{"name", false},
		{"id", false},
		{"created_at", false},
		{"amount", false},

		// docs/plan/43 Task T6: full idempotency keys must never be
		// searchable in logs — idempotency_key/idempotency_scope both
		// normalize to a form containing "idempotencykey".
		{"idempotency_key", true},
		{"idempotencyKey", true},
		{"idempotency_scope", false},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			got := isSensitiveKey(tc.key)
			if got != tc.want {
				t.Errorf("isSensitiveKey(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────
// isSensitiveValue
// ─────────────────────────────────────────────────────────────

func TestIsSensitiveValue(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"Bearer eyJhbGciOiJIUzI1NiJ9", true},
		{"bearer SOMETOKEN", true},
		{"AKIAIOSFODNN7EXAMPLE", true}, // AWS key (16 alphanumeric after AKIA)
		{"-----BEGIN RSA PRIVATE KEY-----", true},
		{"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c", true}, // valid JWT
		{"hello world", false},
		{"john.doe@example.com", false},
		{"normal_value_123", false},
		{"", false},
	}

	for _, tc := range cases {
		t.Run(tc.value[:min(20, len(tc.value))], func(t *testing.T) {
			got := isSensitiveValue(tc.value)
			if got != tc.want {
				t.Errorf("isSensitiveValue(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ─────────────────────────────────────────────────────────────
// maskRecursive / maskPayload
// ─────────────────────────────────────────────────────────────

func TestMaskPayload_RedactsKnownKeys(t *testing.T) {
	input := map[string]any{
		"username": "john",
		"password": "s3cr3t!",
		"email":    "john@example.com",
		"token":    "abc123",
	}
	result := maskPayload(input).(map[string]any)

	if result["username"] != "john" {
		t.Errorf("username should not be redacted, got %v", result["username"])
	}
	if result["email"] != "john@example.com" {
		t.Errorf("email should not be redacted, got %v", result["email"])
	}
	if result["password"] != redacted {
		t.Errorf("password should be redacted, got %v", result["password"])
	}
	if result["token"] != redacted {
		t.Errorf("token should be redacted, got %v", result["token"])
	}
}

func TestMaskPayload_RedactsNestedKeys(t *testing.T) {
	input := map[string]any{
		"user": map[string]any{
			"name":         "alice",
			"access_token": "tok_xyz",
		},
	}
	result := maskPayload(input).(map[string]any)
	user := result["user"].(map[string]any)

	if user["name"] != "alice" {
		t.Errorf("nested name should not be redacted")
	}
	if user["access_token"] != redacted {
		t.Errorf("nested access_token should be redacted, got %v", user["access_token"])
	}
}

func TestMaskPayload_RedactsArrayElements(t *testing.T) {
	input := map[string]any{
		"items": []any{
			map[string]any{"id": 1, "secret": "oops"},
			map[string]any{"id": 2, "value": "ok"},
		},
	}
	result := maskPayload(input).(map[string]any)
	items := result["items"].([]any)

	first := items[0].(map[string]any)
	if first["secret"] != redacted {
		t.Errorf("secret in array element should be redacted")
	}
	second := items[1].(map[string]any)
	if second["value"] != "ok" {
		t.Errorf("value in array element should not be redacted")
	}
}

func TestMaskPayload_PreservesNonStrings(t *testing.T) {
	input := map[string]any{
		"count":  json.Number("42"),
		"active": true,
		"ratio":  json.Number("3.14"),
		"nil":    nil,
	}
	result := maskPayload(input).(map[string]any)

	if result["count"] != json.Number("42") {
		t.Errorf("count should remain json.Number(42), got %T %v", result["count"], result["count"])
	}
	if result["active"] != true {
		t.Errorf("active should remain true")
	}
}

func TestMaskPayload_MaxDepth(t *testing.T) {
	// Bangun nested map sedalam maxDepth+5
	var build func(d int) any
	build = func(d int) any {
		if d == 0 {
			return "leaf"
		}
		return map[string]any{"x": build(d - 1)}
	}
	deep := build(maxDepth + 5)
	result := maskPayload(deep)

	// Telusuri hasilnya — suatu titik harus ada string "[MAX_DEPTH]"
	var traverse func(v any) bool
	traverse = func(v any) bool {
		switch val := v.(type) {
		case string:
			return val == maxDepthStr
		case map[string]any:
			for _, vv := range val {
				if traverse(vv) {
					return true
				}
			}
		}
		return false
	}
	if !traverse(result) {
		t.Error("expected [MAX_DEPTH] somewhere in deeply nested result")
	}
}

func TestMaskPayload_SensitiveStringValue(t *testing.T) {
	// Nilai yang mengandung JWT pattern di field biasa harus di-redact
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	input := map[string]any{
		"data": jwt,
	}
	result := maskPayload(input).(map[string]any)
	if result["data"] != redacted {
		t.Errorf("JWT value should be redacted, got %v", result["data"])
	}
}

// ─────────────────────────────────────────────────────────────
// readBody
// ─────────────────────────────────────────────────────────────

func TestReadBody_PlainText(t *testing.T) {
	body := `{"key":"value"}`
	req := makeRequest(body, "application/json", "")

	got, err := readBody(req, 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != body {
		t.Errorf("got %q, want %q", got, body)
	}

	// Body harus bisa dibaca lagi (restore check)
	restored, _ := io.ReadAll(req.Body)
	if string(restored) != body {
		t.Errorf("body not restored: got %q, want %q", restored, body)
	}
}

func TestReadBody_GzipDecompressed(t *testing.T) {
	original := `{"hello":"gzip"}`
	req := makeRequest(original, "application/json", "gzip")

	got, err := readBody(req, 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != original {
		t.Errorf("got %q, want %q", got, original)
	}

	// Setelah fix: body harus ter-restore dengan konten dekompresi
	restored, _ := io.ReadAll(req.Body)
	if string(restored) != original {
		t.Errorf("body not restored with decompressed content: got %q", restored)
	}
}

func TestReadBody_NilBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	got, err := readBody(req, 1024)
	if err != nil || got != nil {
		t.Errorf("expected nil,nil; got %v,%v", got, err)
	}
}

func TestReadBody_TruncatesAtMax(t *testing.T) {
	body := strings.Repeat("a", 200)
	req := makeRequest(body, "text/plain", "")

	got, err := readBody(req, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) > 100 {
		t.Errorf("expected at most 100 bytes, got %d", len(got))
	}
}

// ─────────────────────────────────────────────────────────────
// ReadAndMaskRequestBody
// ─────────────────────────────────────────────────────────────

func TestReadAndMaskRequestBody_JSON(t *testing.T) {
	payload := `{"username":"alice","password":"hunter2","age":30}`
	req := makeRequest(payload, "application/json", "")

	result := ReadAndMaskRequestBody(req, 1024)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}

	if m["username"] != "alice" {
		t.Errorf("username should be alice, got %v", m["username"])
	}
	if m["password"] != redacted {
		t.Errorf("password should be redacted, got %v", m["password"])
	}
}

func TestReadAndMaskRequestBody_NonJSON(t *testing.T) {
	req := makeRequest("name=alice&age=30", "application/x-www-form-urlencoded", "")

	result := ReadAndMaskRequestBody(req, 1024)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if m["type"] != "non-json-request" {
		fmt.Println()
		t.Errorf("expected type=non-json-request, got %v", m["type"])
	}
}

func TestReadAndMaskRequestBody_InvalidJSON(t *testing.T) {
	req := makeRequest("{not valid json", "application/json", "")

	result := ReadAndMaskRequestBody(req, 1024)
	if result != "[INVALID_JSON]" {
		t.Errorf("expected [INVALID_JSON], got %v", result)
	}
}

func TestReadAndMaskRequestBody_EmptyBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Content-Type", "application/json")

	result := ReadAndMaskRequestBody(req, 1024)
	if result != nil {
		t.Errorf("expected nil for empty body, got %v", result)
	}
}

func TestReadAndMaskRequestBody_GzipJSON(t *testing.T) {
	payload := `{"api_key":"super-secret","user":"bob"}`
	req := makeRequest(payload, "application/json", "gzip")

	result := ReadAndMaskRequestBody(req, 1024)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}

	if m["api_key"] != redacted {
		t.Errorf("api_key should be redacted, got %v", m["api_key"])
	}
	if m["user"] != "bob" {
		t.Errorf("user should be bob, got %v", m["user"])
	}
}

// ─────────────────────────────────────────────────────────────
// MaskResponseBody
// ─────────────────────────────────────────────────────────────

func TestMaskResponseBody_JSON(t *testing.T) {
	body := []byte(`{"user":"alice","token":"abc123","balance":1000}`)
	result := MaskResponseBody(body, "application/json; charset=utf-8")

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if m["user"] != "alice" {
		t.Errorf("user should be alice")
	}
	if m["token"] != redacted {
		t.Errorf("token should be redacted, got %v", m["token"])
	}
	if m["balance"] != json.Number("1000") {
		t.Errorf("balance should be 1000, got %v", m["balance"])
	}
}

func TestMaskResponseBody_NonJSON(t *testing.T) {
	body := []byte("<html><body>hello</body></html>")
	result := MaskResponseBody(body, "text/html; charset=utf-8")

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if m["type"] != "non-json-response" {
		t.Errorf("expected type=non-json-response, got %v", m["type"])
	}
}

func TestMaskResponseBody_InvalidJSON(t *testing.T) {
	body := []byte("{broken")
	result := MaskResponseBody(body, "application/json")
	if result != "[INVALID_JSON_RESPONSE]" {
		t.Errorf("expected [INVALID_JSON_RESPONSE], got %v", result)
	}
}

// ─────────────────────────────────────────────────────────────
// SanitizeHeaders
// ─────────────────────────────────────────────────────────────

func TestSanitizeHeaders_BlockedHeaders(t *testing.T) {
	headers := http.Header{
		"Authorization":       []string{"Bearer tok123"},
		"Cookie":              []string{"session=abc"},
		"X-Api-Key":           []string{"my-api-key"},
		"X-Auth-Token":        []string{"secret"},
		"Proxy-Authorization": []string{"Basic dXNlcjpwYXNz"},
		"Set-Cookie":          []string{"id=a3fWa; Expires=Wed"},
	}
	result := SanitizeHeaders(headers)

	for _, key := range []string{"Authorization", "Cookie", "X-Api-Key", "X-Auth-Token", "Proxy-Authorization", "Set-Cookie"} {
		if result[key] != redacted {
			t.Errorf("header %q should be [REDACTED], got %q", key, result[key])
		}
	}
}

func TestSanitizeHeaders_AllowedHeaders(t *testing.T) {
	headers := http.Header{
		"Content-Type":    []string{"application/json"},
		"Accept":          []string{"*/*"},
		"X-Request-Id":    []string{"req-123"},
		"X-Forwarded-For": []string{"10.0.0.1"},
	}
	result := SanitizeHeaders(headers)

	if result["Content-Type"] != "application/json" {
		t.Errorf("Content-Type should pass through, got %q", result["Content-Type"])
	}
	if result["X-Request-Id"] != "req-123" {
		t.Errorf("X-Request-Id should pass through, got %q", result["X-Request-Id"])
	}
}

func TestSanitizeHeaders_SensitiveValue(t *testing.T) {
	// Header kustom yang membawa JWT harus di-redact via isSensitiveValue
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	headers := http.Header{
		"X-Custom-Jwt": []string{jwt},
	}
	result := SanitizeHeaders(headers)

	if result["X-Custom-Jwt"] != redacted {
		t.Errorf("header with JWT value should be [REDACTED], got %q", result["X-Custom-Jwt"])
	}
}

func TestSanitizeHeaders_MultipleValues(t *testing.T) {
	headers := http.Header{
		"Accept": []string{"text/html", "application/json"},
	}
	result := SanitizeHeaders(headers)

	if result["Accept"] != "text/html,application/json" {
		t.Errorf("multiple header values should be joined, got %q", result["Accept"])
	}
}

func TestSanitizeHeaders_CaseInsensitiveBlocking(t *testing.T) {
	headers := http.Header{
		"AUTHORIZATION": []string{"Bearer abc"},
	}
	result := SanitizeHeaders(headers)

	// http.CanonicalHeaderKey akan jadikan "Authorization"
	if result["Authorization"] != redacted {
		t.Errorf("AUTHORIZATION (uppercase) should still be blocked, got %q", result["Authorization"])
	}
}

// ─────────────────────────────────────────────────────────────
// parseMediaType
// ─────────────────────────────────────────────────────────────

func TestParseMediaType(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"application/json", "application/json"},
		{"application/json; charset=utf-8", "application/json"},
		{"text/html; charset=utf-8", "text/html"},
		{"", ""},
	}

	for _, tc := range cases {
		got := parseMediaType(tc.input)
		if got != tc.want {
			t.Errorf("parseMediaType(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Regression: bug fix isSensitiveKey dengan separator
// ─────────────────────────────────────────────────────────────

func TestIsSensitiveKey_Regression_SeparatorBug(t *testing.T) {
	// Versi lama: HasPrefix(k, s+"_") tidak pernah match karena k sudah di-strip
	// Versi baru: strings.Contains harus mendeteksi ini
	separatorVariants := []string{
		"user_password",
		"user-password",
		"access_token",
		"refresh-token",
		"api_key",
		"db_secret",
	}
	for _, key := range separatorVariants {
		if !isSensitiveKey(key) {
			t.Errorf("regression: isSensitiveKey(%q) should be true (separator bug fix)", key)
		}
	}
}
