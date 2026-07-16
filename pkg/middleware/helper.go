package middleware

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	"github.com/herdifirdausss/seev/pkg/response"
)

type contextKey string

const (
	RequestIDKey   contextKey = "request_id"
	UserIDKey      contextKey = "user_id"
	maxCaptureSize            = 1 << 20 // 1MB
)

// RequestIDFromCtx returns the request ID stored in ctx.
func RequestIDFromCtx(ctx context.Context) string {
	id, _ := ctx.Value(RequestIDKey).(string)
	return id
}

// UserIDFromCtx returns the authenticated user ID stored in ctx.
func UserIDFromCtx(ctx context.Context) string {
	id, _ := ctx.Value(UserIDKey).(string)
	return id
}

// Middleware is a function that wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain composes multiple middleware; first in list = outermost wrapper.
func Chain(middlewares ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}

// ─── Internal ─────────────────────────────────────────────────────────────────

// captureWriter wraps http.ResponseWriter to capture the status code.
type captureWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
	wroteHeader  bool
	body         bytes.Buffer
}

func (rw *captureWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.statusCode = code
		rw.wroteHeader = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *captureWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}

	if rw.body.Len() < maxCaptureSize {
		rw.body.Write(b)
	}

	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

// ─── Content-Type enforcement ─────────────────────────────────────────────────

// RequireJSON rejects mutation requests without Content-Type: application/json
// — except multipart/form-data, the one legitimate non-JSON body this API
// accepts (file uploads, e.g. POST /admin/recon/batches's CSV import,
// docs/plan/16 Task T2). Rejecting multipart here would 400 every such
// upload before it ever reaches the handler that's supposed to parse it.
func RequireJSON() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
				ct := r.Header.Get("Content-Type")
				if !strings.HasPrefix(ct, "application/json") && !strings.HasPrefix(ct, "multipart/form-data") {
					response.BadRequest(w, "Content-Type must be application/json or multipart/form-data")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// realIP extracts the real client IP, respecting common proxy headers.
func realIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-Ip"); ip != "" {
		return ip
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.SplitN(fwd, ",", 2)[0]
	}
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i != -1 {
		return addr[:i]
	}
	return addr
}
