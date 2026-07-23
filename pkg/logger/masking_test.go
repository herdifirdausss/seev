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
		// Exact matches after normalization.
		{"password", true},
		{"PASSWORD", true},
		{"token", true},
		{"jwt", true},
		{"pin", true},

		// With separators — this was a bug in the old version and is now fixed.
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

		// Not sensitive.
		{"username", false},
		{"email", false},
		{"name", false},
		{"id", false},
		{"created_at", false},
		{"amount", false},

		// docs/roadmap/archive/43 Task T6: full idempotency keys must never be
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
		{syntheticJWT(), true},
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

	// Walk the result — at least one node must contain "[MAX_DEPTH]".
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
	// Values containing a JWT pattern in ordinary fields must be redacted.
	jwt := syntheticJWT()
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

	// The body must remain readable (restore check).
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

	// After the fix: the body must be restored with decompressed content.
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

// TestReadBody_RestoresFullBodyDespiteLogTruncation proves docs/roadmap/archive/49
// TM-12: a body far larger than the log-line `max` must still be read in
// FULL by whatever reads r.Body next — the previous implementation
// silently truncated r.Body itself to `max`, corrupting any downstream
// HMAC signature check or multipart parse for a body that was legitimately
// within its OWN handler's larger limit (e.g. the ledger reconciliation
// CSV upload's 10MiB cap).
func TestReadBody_RestoresFullBodyDespiteLogTruncation(t *testing.T) {
	full := strings.Repeat("x", 50_000) // far past the 16KiB log-line max
	req := makeRequest(full, "text/plain", "")

	logCopy, err := readBody(req, 16*1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logCopy) != 16*1024 {
		t.Errorf("log copy: expected exactly 16KiB, got %d", len(logCopy))
	}

	restored, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("reading restored r.Body: %v", err)
	}
	if len(restored) != len(full) {
		t.Fatalf("r.Body restored with %d bytes, want the full %d — downstream handler would see a corrupted/truncated body", len(restored), len(full))
	}
	if string(restored) != full {
		t.Fatal("r.Body restored with different content than the original request")
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
	// A custom header carrying a JWT must be redacted via isSensitiveValue.
	jwt := syntheticJWT()
	headers := http.Header{
		"X-Custom-Jwt": []string{jwt},
	}
	result := SanitizeHeaders(headers)

	if result["X-Custom-Jwt"] != redacted {
		t.Errorf("header with JWT value should be [REDACTED], got %q", result["X-Custom-Jwt"])
	}
}

func syntheticJWT() string {
	const (
		header    = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"
		payload   = "eyJzdWIiOiIxMjM0NTY3ODkwIn0"
		signature = "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	)
	return header + "." + payload + "." + signature
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

	// http.CanonicalHeaderKey canonicalizes this to "Authorization".
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
// Regression: isSensitiveKey separator bug fix.
// ─────────────────────────────────────────────────────────────

func TestIsSensitiveKey_Regression_SeparatorBug(t *testing.T) {
	// Old version: HasPrefix(k, s+"_") never matched because k was stripped.
	// New version: strings.Contains must detect this case.
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
