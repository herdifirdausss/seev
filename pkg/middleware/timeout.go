package middleware

import (
	"context"
	"net/http"
	"time"
)

// ─── Timeout ──────────────────────────────────────────────────────────────────

// WithTimeout cancels the request context after the given duration.
func WithTimeout(timeout time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
