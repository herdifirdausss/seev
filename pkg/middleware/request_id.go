package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// ─── Request ID ───────────────────────────────────────────────────────────────

const maxRequestIDLen = 64

// isValidRequestID restricts inbound ids to a safe charset/length — this is a
// public edge, an unsanitized client-supplied id would otherwise flow
// unescaped into every downstream log line (log poisoning) and storage
// column (docs/plan/36 Task T1).
func isValidRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// WithRequestID injects a unique request ID into the context and X-Request-Id
// header. The id is also written back onto r.Header so that downstream
// forwarders (e.g. the gateway's reverse proxy to ledger-service) propagate a
// gateway-generated id, not just a client-supplied one — previously it was
// only echoed on the response, so a generated id never reached ledger-service.
func WithRequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if !isValidRequestID(id) {
				id = uuid.New().String()
			}
			r.Header.Set("X-Request-Id", id)
			ctx := context.WithValue(r.Context(), RequestIDKey, id)
			w.Header().Set("X-Request-Id", id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
