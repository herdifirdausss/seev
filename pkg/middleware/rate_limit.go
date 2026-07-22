package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/herdifirdausss/seev/pkg/cache"
)

// ─── Rate Limiter ─────────────────────────────────────────────────────────────

func WithRateLimit(
	limiter cache.Limiter,
	keyFn func(*http.Request) string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)

			allowed, remaining, err := limiter.Allow(r.Context(), key)
			if err != nil {
				// Fail-open: a rate limiter error lets the request through
				// rather than blocking it. This is a deliberate, retained
				// decision (docs/plan/12 Task T1) — rate limiting exists to
				// defend against DoS/abuse, it is not a financial control,
				// so letting traffic through on error cannot cause money
				// loss (unlike a fail-open on a balance lock or idempotency
				// check, which would be unacceptable). With the in-memory
				// fallback (cache.MemoryRateLimiter) wired in whenever
				// Redis is disabled or down at startup, limiter.Allow
				// itself essentially never errors anymore in normal
				// operation — this path only matters for a mid-flight
				// Redis outage while RedisRateLimiter is still configured.
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("X-RateLimit-Remaining", fmt.Sprint(remaining))
			w.Header().Set("X-RateLimit-Allowed", fmt.Sprint(allowed))

			if !allowed {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate limit exceeded","message":"Too many requests","success":false}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func RateLimitByIP(r *http.Request) string {
	return "rl:ip:" + rateLimitIP(r)
}

// rateLimitIP strips the ephemeral client port from r.RemoteAddr so that a
// client's rate-limit bucket survives across new TCP connections (docs/plan/49
// TM-11). Deliberately does NOT trust X-Forwarded-For/X-Real-Ip the way the
// logger's realIP helper does: those headers are client-suppliable, and
// honoring them here would let an attacker rotate the rate-limit key on every
// request just by changing a header, which is strictly easier than the
// port-rotation bypass this fix closes.
func rateLimitIP(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i != -1 {
		return addr[:i]
	}
	return addr
}

// RateLimitByUser keys by the authenticated user id set by WithAuth. Falls
// back to RateLimitByIP if the request somehow has no user id in context
// (e.g. called before WithAuth ran) — still rate-limited, just not per-user
// for that one request, rather than colliding every such request onto a
// single "rl:user:" bucket. docs/plan/12 Task T6: the previous version used
// r.Context().Value("user_id").(string) — a context key comparison is by
// both type AND value, so a plain string "user_id" never matches the typed
// contextKey("user_id") WithAuth actually stores under (UserIDKey). That
// always returned a nil interface, and the unchecked .(string) assertion on
// it panicked on every single call — not currently reachable from any
// registered route, but safe to wire up now.
func RateLimitByUser(r *http.Request) string {
	if userID := UserIDFromCtx(r.Context()); userID != "" {
		return "rl:user:" + userID
	}
	return RateLimitByIP(r)
}

func RateLimitByIPAndPath(r *http.Request) string {
	return "rl:" + rateLimitIP(r) + ":" + r.URL.Path
}

// RateLimitByVendor keys by the {vendor} path value, not the caller's IP —
// a payment vendor can deliver webhooks from many source IPs, so per-IP
// keying would under-limit a single noisy/misbehaving vendor while
// over-limiting nothing in particular (docs/plan/22 Task T3).
func RateLimitByVendor(r *http.Request) string {
	return "rl:webhook:" + r.PathValue("vendor")
}
