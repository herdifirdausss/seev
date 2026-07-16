package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/herdifirdausss/seev/pkg/response"
)

// ─── Recovery ─────────────────────────────────────────────────────────────────

// WithRecovery catches panics and returns HTTP 500.
func WithRecovery() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					slog.Error("panic recovered",
						"request_id", RequestIDFromCtx(r.Context()),
						"panic", p,
						"stack", string(debug.Stack()),
					)
					response.InternalServerError(w, fmt.Errorf("panic: %v", p))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
