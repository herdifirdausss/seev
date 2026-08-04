package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// okHandler returns a simple 200 OK handler for chaining tests.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// ─── Chain ────────────────────────────────────────────────────────────────────

func TestChain_OrderIsPreserved(t *testing.T) {
	var order []int

	mw := func(n int) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, n)
				next.ServeHTTP(w, r)
			})
		}
	}

	handler := Chain(mw(1), mw(2), mw(3))(okHandler())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, []int{1, 2, 3}, order)
}

// ─── Context helpers ──────────────────────────────────────────────────────────

func TestRequestIDFromCtx_Present(t *testing.T) {
	ctx := context.WithValue(context.Background(), RequestIDKey, "abc-123")
	assert.Equal(t, "abc-123", RequestIDFromCtx(ctx))
}

func TestRequestIDFromCtx_Missing(t *testing.T) {
	assert.Equal(t, "", RequestIDFromCtx(context.Background()))
}

func TestUserIDFromCtx_Present(t *testing.T) {
	ctx := context.WithValue(context.Background(), UserIDKey, "user-1")
	assert.Equal(t, "user-1", UserIDFromCtx(ctx))
}

func TestUserIDFromCtx_Missing(t *testing.T) {
	assert.Equal(t, "", UserIDFromCtx(context.Background()))
}

// ─── RequireJSON ──────────────────────────────────────────────────────────────

func TestRequireJSON_AllowsWithCorrectContentType(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			handler := RequireJSON()(okHandler())
			req := httptest.NewRequest(method, "/", nil)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

func TestRequireJSON_RejectsWrongContentType(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			handler := RequireJSON()(okHandler())
			req := httptest.NewRequest(method, "/", nil)
			req.Header.Set("Content-Type", "text/plain")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestRequireJSON_AllowsMultipartFormData(t *testing.T) {
	// File uploads (e.g. POST /admin/recon/batches's CSV import,
	// docs/roadmap/archive/16 Task T2) are the one legitimate non-JSON body.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			handler := RequireJSON()(okHandler())
			req := httptest.NewRequest(method, "/", nil)
			req.Header.Set("Content-Type", "multipart/form-data; boundary=abc123")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

func TestRequireJSON_AllowsGetWithoutContentType(t *testing.T) {
	handler := RequireJSON()(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireJSON_AllowsDeleteWithoutContentType(t *testing.T) {
	handler := RequireJSON()(okHandler())
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ─── captureWriter ────────────────────────────────────────────────────────────

func TestCaptureWriter_DefaultStatus(t *testing.T) {
	w := httptest.NewRecorder()
	cw := &captureWriter{ResponseWriter: w, statusCode: http.StatusOK}

	// Write without explicit WriteHeader should use 200
	_, err := cw.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, cw.statusCode)
	assert.Equal(t, 5, cw.bytesWritten)
}

func TestCaptureWriter_WriteHeaderOnce(t *testing.T) {
	w := httptest.NewRecorder()
	cw := &captureWriter{ResponseWriter: w, statusCode: http.StatusOK}

	cw.WriteHeader(http.StatusCreated)
	cw.WriteHeader(http.StatusOK) // second call ignored
	assert.Equal(t, http.StatusCreated, cw.statusCode)
}

// ─── realIP ───────────────────────────────────────────────────────────────────

func TestRealIP_XRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-Ip", "1.2.3.4")
	assert.Equal(t, "1.2.3.4", realIP(req))
}

func TestRealIP_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "5.6.7.8, 9.10.11.12")
	assert.Equal(t, "5.6.7.8", realIP(req))
}

func TestRealIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:54321"
	assert.Equal(t, "10.0.0.1", realIP(req))
}

func TestRealIP_RemoteAddr_NoPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1"
	assert.Equal(t, "10.0.0.1", realIP(req))
}
