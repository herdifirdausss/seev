package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ─── WithRequestID ────────────────────────────────────────────────────────────

func TestWithRequestID_GeneratesID(t *testing.T) {
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromCtx(r.Context())
	})

	handler := WithRequestID()(inner)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.NotEmpty(t, capturedID)
	assert.Equal(t, capturedID, w.Header().Get("X-Request-Id"))
}

func TestWithRequestID_RespectsExistingHeader(t *testing.T) {
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromCtx(r.Context())
	})

	handler := WithRequestID()(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "upstream-id-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, "upstream-id-123", capturedID)
	assert.Equal(t, "upstream-id-123", w.Header().Get("X-Request-Id"))
}

// GeneratedIDPropagatesToDownstreamHeader is the docs/roadmap/archive/36 T1 regression:
// a generated (not client-supplied) id must land on r.Header too, so a
// reverse proxy forwarding the request carries it downstream — previously it
// was only echoed on the response.
func TestWithRequestID_GeneratedIDPropagatesToDownstreamHeader(t *testing.T) {
	var headerID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerID = r.Header.Get("X-Request-Id")
	})

	handler := WithRequestID()(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.NotEmpty(t, headerID)
	assert.Equal(t, w.Header().Get("X-Request-Id"), headerID)
}

func TestWithRequestID_RejectsOversizedID(t *testing.T) {
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromCtx(r.Context())
	})

	handler := WithRequestID()(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", strings.Repeat("a", 65))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.NotEqual(t, strings.Repeat("a", 65), capturedID)
	assert.LessOrEqual(t, len(capturedID), maxRequestIDLen)
}

func TestWithRequestID_RejectsInvalidCharset(t *testing.T) {
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromCtx(r.Context())
	})

	handler := WithRequestID()(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "id-with-\r\ninjected\theader\nvalue")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.NotContains(t, capturedID, "\r")
	assert.NotContains(t, capturedID, "\n")
}

func TestWithRequestID_AcceptsValidCharset(t *testing.T) {
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromCtx(r.Context())
	})

	handler := WithRequestID()(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "abc123._-XYZ")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, "abc123._-XYZ", capturedID)
}
