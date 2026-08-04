package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/repository"
)

// lastUsedSampleInterval is §8.5's "no more frequently than once per
// configured interval" — five minutes, per the plan's own stated initial
// value. This alone does not skip a touch; TouchIfStale does, using the
// key's own LastUsedAt.
const lastUsedSampleInterval = 5 * time.Minute

// RequireMerchantAuth implements T3 §8.3's full lookup flow as HTTP
// middleware: parse -> prefix lookup -> digest compare -> tenant/key/
// expiry checks -> construct Principal. Every failure path returns 401
// (never leaking WHICH check failed in the response body — only in
// server-side logs). A suspended tenant is NOT a failure here — the key
// itself is valid and §23.7 requires reads to keep working for
// reconciliation — it authenticates with Principal.TenantSuspended set;
// RequireTenantNotSuspendedForWrites is what turns that into a 403 for
// writes.
//
// pepper must be non-empty — RequireMerchantAuth panics at construction
// (boot time) otherwise, matching this repository's fail-closed
// convention for missing secrets (never silently accept every key as
// valid because the pepper was unset).
func RequireMerchantAuth(keys repository.APIKeyRepository, tenants repository.TenantRepository, pepper string) middleware.Middleware {
	if pepper == "" {
		panic("merchant/auth: RequireMerchantAuth requires a non-empty pepper")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if authHeader == "" || !strings.HasPrefix(authHeader, prefix) {
				response.Unauthorized(w, "authentication required")
				return
			}
			raw := strings.TrimPrefix(authHeader, prefix)

			parsed, err := ParseKey(raw)
			if err != nil {
				response.Unauthorized(w, "invalid API key")
				return
			}

			key, err := keys.GetActiveByPrefix(r.Context(), parsed.PublicPrefix)
			if errors.Is(err, repository.ErrNotFound) {
				// Covers: unknown prefix, revoked, AND expired-by-status
				// keys identically — GetActiveByPrefix's own WHERE status
				// = 'active' clause means all three read as "not found"
				// here, so this response never distinguishes them.
				response.Unauthorized(w, "invalid API key")
				return
			}
			if err != nil {
				response.Unauthorized(w, "invalid API key")
				return
			}

			// Environment mismatch: a sandbox key parsed with a "live"
			// prefix, or vice versa, cannot happen from ParseKey's own
			// output (the prefix IS the environment marker baked into
			// public_prefix), but a defensive check stays cheap insurance
			// against a future refactor of the prefix format.
			if key.Environment != parsed.Environment {
				response.Unauthorized(w, "invalid API key")
				return
			}

			match, err := VerifyDigest(pepper, raw, key.SecretDigest)
			if err != nil || !match {
				response.Unauthorized(w, "invalid API key")
				return
			}

			if key.ExpiresAt != nil && !key.ExpiresAt.After(timeNow()) {
				response.Unauthorized(w, "invalid API key")
				return
			}

			tenant, err := tenants.GetByID(r.Context(), key.TenantID)
			if err != nil {
				response.Unauthorized(w, "invalid API key")
				return
			}
			// Defense in depth for §23.7's "test key accesses live tenant" /
			// "live key accesses sandbox tenant": CreateKey (T10) now
			// refuses to issue a key whose environment doesn't match its
			// tenant's, but this catches any key that predates that fix
			// too — fail closed exactly like every other check here,
			// never distinguishing the reason in the response body.
			if key.Environment != tenant.Environment {
				response.Unauthorized(w, "invalid API key")
				return
			}
			// §23.7's suspension policy is "reads remain available for
			// reconciliation, writes are denied" — a suspended tenant's
			// otherwise-valid key must still authenticate here so a GET
			// can proceed; RequireTenantNotSuspendedForWrites (mounted
			// per-route, since only the router knows isWrite) is what
			// actually blocks writes. draft/closed tenants get no such
			// carve-out: fail closed identically to "not found", matching
			// every other non-active, non-suspended status.
			suspended := tenant.Status == "suspended"
			if tenant.Status != "active" && !suspended {
				response.Unauthorized(w, "invalid API key")
				return
			}

			if key.LastUsedAt == nil || timeNow().Sub(*key.LastUsedAt) >= lastUsedSampleInterval {
				// Best-effort, never blocks the request on failure —
				// losing one sampled timestamp update is not worth
				// failing an otherwise-valid authenticated request.
				_ = keys.TouchLastUsed(r.Context(), key.ID)
			}

			principal := Principal{TenantID: tenant.ID, KeyID: key.ID, Environment: key.Environment, Scopes: key.Scopes, TenantSuspended: suspended}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
		})
	}
}

// RequireTenantNotSuspendedForWrites enforces §23.7's suspension policy on
// a single route: a suspended tenant's reads remain available (for
// reconciliation) but every write is denied. Must run after
// RequireMerchantAuth (reads Principal.TenantSuspended from context) —
// isWrite comes from the route table, the same way quota.RequireQuota
// already receives it, since suspension enforcement needs to know per
// ROUTE, not per tenant, whether a request is a write.
func RequireTenantNotSuspendedForWrites(isWrite bool) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				response.Unauthorized(w, "authentication required")
				return
			}
			if principal.TenantSuspended && isWrite {
				response.ErrorStatus(w, http.StatusForbidden, "TENANT_SUSPENDED", "tenant suspended: financial and management writes are denied")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireScope wraps a handler with a scope check against the
// already-authenticated Principal in context — must be mounted AFTER
// RequireMerchantAuth in the middleware chain. operationID is looked up
// in the central scope registry (§7.2); an unregistered operationID fails
// closed (403), never treated as "no scope required."
func RequireScope(operationID string) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				response.Unauthorized(w, "authentication required")
				return
			}
			required, ok := RequiredScopes(operationID)
			if !ok || !AuthorizeScopes(principal, required) {
				response.Forbidden(w, "insufficient scope")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// timeNow is a seam for deterministic expiry tests.
var timeNow = time.Now
