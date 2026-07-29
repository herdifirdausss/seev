package quota

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/herdifirdausss/seev/internal/merchant/auth"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

// RequireQuota enforces a per-tenant quota before running next — must be
// mounted AFTER auth.RequireMerchantAuth (it reads the Principal already
// populated in context). isWrite selects the fail-closed-vs-degraded-read
// posture (see Enforcer.Check).
func RequireQuota(enforcer *Enforcer, quotaClass string, isWrite bool) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := auth.PrincipalFromContext(r.Context())
			if !ok {
				response.Unauthorized(w, "authentication required")
				return
			}

			result, err := enforcer.Check(r.Context(), principal.TenantID, quotaClass, isWrite)
			if err != nil {
				if errors.Is(err, ErrQuotaBackendUnavailable) {
					// §6.3: Retry-After required on 429, but this is a 503 —
					// still worth a bounded Retry-After so a well-behaved
					// client backs off instead of hammering a down backend.
					w.Header().Set("Retry-After", "5")
					response.ServiceUnavailable(w, "QUOTA_UNAVAILABLE", "quota backend unavailable; write rejected (fail-closed)")
					return
				}
				response.ServiceUnavailable(w, "QUOTA_UNAVAILABLE", "quota check failed")
				return
			}

			setRateLimitHeaders(w, result)
			if !result.Allowed {
				w.Header().Set("Retry-After", strconv.FormatInt(max(result.ResetSeconds, 1), 10))
				response.TooManyRequests(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func setRateLimitHeaders(w http.ResponseWriter, result Result) {
	w.Header().Set("RateLimit-Limit", strconv.FormatInt(result.Limit, 10))
	w.Header().Set("RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
	w.Header().Set("RateLimit-Reset", strconv.FormatInt(result.ResetSeconds, 10))
}
