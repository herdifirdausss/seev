package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ─── WithSecurityHeaders ──────────────────────────────────────────────────────

func TestWithSecurityHeaders(t *testing.T) {
	handler := WithSecurityHeaders(DefaultSecurityHeadersConfig())(okHandler())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.NotEmpty(t, w.Header().Get("Content-Security-Policy"))
	assert.NotEmpty(t, w.Header().Get("Referrer-Policy"))
	assert.NotEmpty(t, w.Header().Get("Permissions-Policy"))
}

// ─── HSTS trust-proxy (docs/roadmap/archive/10 Task T6) ───────────────────────────────────

func TestWithSecurityHeaders_HSTS_PlainHTTP_NoTrustProxy_NotSet(t *testing.T) {
	handler := WithSecurityHeaders(DefaultSecurityHeadersConfig())(okHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https") // must be ignored — TrustProxyHeaders is off
	handler.ServeHTTP(w, req)

	assert.Empty(t, w.Header().Get("Strict-Transport-Security"))
}

func TestWithSecurityHeaders_HSTS_TrustProxy_ForwardedHTTPS_Set(t *testing.T) {
	cfg := DefaultSecurityHeadersConfig()
	cfg.TrustProxyHeaders = true
	handler := WithSecurityHeaders(cfg)(okHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	handler.ServeHTTP(w, req)

	assert.NotEmpty(t, w.Header().Get("Strict-Transport-Security"))
}

func TestWithSecurityHeaders_HSTS_TrustProxy_ForwardedHTTP_NotSet(t *testing.T) {
	cfg := DefaultSecurityHeadersConfig()
	cfg.TrustProxyHeaders = true
	handler := WithSecurityHeaders(cfg)(okHandler())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	handler.ServeHTTP(w, req)

	assert.Empty(t, w.Header().Get("Strict-Transport-Security"))
}
