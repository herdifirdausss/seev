package auth

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
)

// TestRequireMerchantAuth_KeyPlaintextNeverAppearsInLogs is T3's own
// required log-capture test ("log-capture tests find no key plaintext").
// It chains internal/platform/security/middleware.WithLogger (the same request-logging
// middleware every other route in this repository already uses) in front
// of RequireMerchantAuth, sends a real plaintext key on the Authorization
// header, and asserts the captured log output never contains that
// plaintext — proving the existing SanitizeHeaders masking (already
// covering "authorization" generically) actually protects this new
// header value too, not just JWT bearer tokens.
func TestRequireMerchantAuth_KeyPlaintextNeverAppearsInLogs(t *testing.T) {
	keys, tenants := newFakeKeyRepo(), newFakeTenantRepo()
	plaintext, _, _ := seedKeyAndTenant(t, keys, tenants, "active", "active", nil, []string{"merchant:read"})

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := middleware.WithLogger(logger)(
		RequireMerchantAuth(keys, tenants, testPepper)(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}),
		),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/b2b/merchant", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	logOutput := buf.String()
	require.NotEmpty(t, logOutput, "the request logging middleware must have written something")
	assert.NotContains(t, logOutput, plaintext, "the full plaintext key must never appear in logs")

	// A weaker but still meaningful check: not even the bare secret
	// portion (after the last verified-safe fixed-length prefix) should
	// leak, in case some future log line serializes the header value
	// differently than the raw string.
	secretPortion := plaintext[len(plaintext)-secretLen:]
	assert.NotContains(t, logOutput, secretPortion, "the key's secret portion must never appear in logs")
}

// TestRequireMerchantAuth_TamperedKeyAttemptNeverLogsPlaintext proves the
// same property on the FAILURE path (an invalid key attempt) — an
// attacker's failed guesses must not accidentally get echoed into logs
// either.
func TestRequireMerchantAuth_TamperedKeyAttemptNeverLogsPlaintext(t *testing.T) {
	keys, tenants := newFakeKeyRepo(), newFakeTenantRepo()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := middleware.WithLogger(logger)(
		RequireMerchantAuth(keys, tenants, testPepper)(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("handler must not run for an invalid key")
			}),
		),
	)

	const attemptedKey = "sk_test_totallymadeupprefix_andasecretguess"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/b2b/merchant", nil)
	req.Header.Set("Authorization", "Bearer "+attemptedKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	logOutput := buf.String()
	assert.False(t, strings.Contains(logOutput, attemptedKey), "a rejected key attempt must never appear in logs")
}
