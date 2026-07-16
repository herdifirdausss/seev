package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ─── WithCORS ─────────────────────────────────────────────────────────────────

func TestWithCORS_SetsHeaders(t *testing.T) {
	handler := WithCORS(DefaultCORSConfig())(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Methods"))
	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"))
}

func TestWithCORS_HandlesPreflight(t *testing.T) {
	handler := WithCORS(DefaultCORSConfig())(okHandler())

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestWithCORS_WildcardOrigin(t *testing.T) {
	cfg := DefaultCORSConfig()
	cfg.AllowedOrigins = []string{"*"}
	handler := WithCORS(cfg)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://anywhere.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	// Wildcard: origin header is echoed back
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestWithCORS_SpecificOriginAllowed(t *testing.T) {
	cfg := CORSConfig{
		AllowedOrigins:   []string{"https://allowed.com"},
		AllowedMethods:   []string{"GET"},
		AllowedHeaders:   []string{},
		ExposedHeaders:   []string{},
		AllowCredentials: true,
		MaxAge:           3600,
	}
	handler := WithCORS(cfg)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://allowed.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, "https://allowed.com", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
}

func TestWithCORS_OriginNotAllowed(t *testing.T) {
	cfg := CORSConfig{
		AllowedOrigins: []string{"https://allowed.com"},
		AllowedMethods: []string{"GET"},
		MaxAge:         0,
	}
	handler := WithCORS(cfg)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	// Not allowed origin → header not set
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestWithCORS_NoExposedHeaders(t *testing.T) {
	cfg := CORSConfig{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET"},
		AllowedHeaders: []string{},
		ExposedHeaders: []string{}, // empty — header should not be set
	}
	handler := WithCORS(cfg)(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://x.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Empty(t, w.Header().Get("Access-Control-Expose-Headers"))
}
