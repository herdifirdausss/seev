package auth

import (
	"context"
	"slices"

	"github.com/google/uuid"
)

// Principal is the machine identity resolved from a validated merchant API
// key (T3 §8.3 step 7: "construct a machine principal containing tenant
// ID, key ID, environment, and scopes"). It is never derived from or
// convertible to internal/platform/security/middleware.Claims — a distinct type is the point
// (§3.2).
type Principal struct {
	TenantID    uuid.UUID
	KeyID       uuid.UUID
	Environment string // "sandbox" | "live"
	Scopes      []string
	// TenantSuspended is true when the owning tenant's status is
	// "suspended" — §23.7's policy is reads remain available (for
	// reconciliation) while writes are denied, so RequireMerchantAuth lets
	// a suspended tenant's otherwise-valid key authenticate; a suspended
	// tenant must never be treated as simply "not found" the way
	// draft/closed tenants are. RequireTenantNotSuspendedForWrites is the
	// middleware that actually enforces the read/write split using this
	// field.
	TenantSuspended bool
}

// HasScope reports whether the principal was issued the given scope.
// Scope evaluation happens strictly after key and tenant validation
// (§7.2) — callers must never check HasScope before authentication has
// already populated a Principal.
func (p Principal) HasScope(scope string) bool {
	return slices.Contains(p.Scopes, scope)
}

type principalKey struct{}

// WithPrincipal stores p in ctx for downstream handlers/use cases.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFromContext retrieves the Principal stored by WithMerchantAuth.
// ok=false means no principal was ever set — callers must treat this as
// unauthenticated, never assume a zero-value Principal is a valid
// low-privilege actor.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
