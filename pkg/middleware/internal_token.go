package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/herdifirdausss/seev/pkg/response"
)

// WithInternalToken is docs/roadmap/active/51-a8-data-lifecycle-privacy.md T5's HTTP analog of
// pkg/grpcx's own authInterceptor(token) — this codebase's gRPC surface
// has required a shared internal credential (INTERNAL_GRPC_TOKEN,
// docs/roadmap/archive/49 K5, fail-closed on empty) since doc 49, but no HTTP
// equivalent existed until a service (auth, orchestrating the K10 closure
// saga) needed to call another service's own additive internal HTTP
// endpoint as a genuine machine caller — mTLS alone proves WHICH SERVICE
// is calling (pkg/tlsx.ServerConfig's identity allowlist), not that the
// call is an authorized internal one rather than, say, a compromised
// but correctly-identified peer replaying a captured request.
//
// token must be non-empty — same fail-closed posture as
// pkg/grpcx.NewServer's own refusal to construct a server with an empty
// token at all, applied here at request time since HTTP middleware has no
// equivalent "refuse to even start" construction step.
func WithInternalToken(token string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				response.Unauthorized(w, "internal token not configured")
				return
			}
			const prefix = "Bearer "
			header := r.Header.Get("Authorization")
			if len(header) != len(prefix)+len(token) || header[:len(prefix)] != prefix ||
				subtle.ConstantTimeCompare([]byte(header[len(prefix):]), []byte(token)) != 1 {
				response.Unauthorized(w, "invalid internal token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
