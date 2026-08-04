package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithInternalToken_ValidTokenPasses(t *testing.T) {
	called := false
	handler := WithInternalToken("secret-token")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/internal/privacy/closure/prepare", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.True(t, called)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestWithInternalToken_WrongTokenRejected(t *testing.T) {
	called := false
	handler := WithInternalToken("secret-token")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodPost, "/internal/privacy/closure/prepare", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.False(t, called)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWithInternalToken_MissingHeaderRejected(t *testing.T) {
	handler := WithInternalToken("secret-token")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must never run without a valid token")
	}))
	req := httptest.NewRequest(http.MethodPost, "/internal/privacy/closure/prepare", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestWithInternalToken_EmptyConfiguredTokenFailsClosed mirrors
// internal/platform/transport/grpc.NewServer's own refusal to run with an empty token
// (docs/roadmap/archive/49 K5) — every request must be rejected, never silently
// let through because nothing was configured.
func TestWithInternalToken_EmptyConfiguredTokenFailsClosed(t *testing.T) {
	handler := WithInternalToken("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must never run when no internal token is configured")
	}))
	req := httptest.NewRequest(http.MethodPost, "/internal/privacy/closure/prepare", nil)
	req.Header.Set("Authorization", "Bearer anything")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}
