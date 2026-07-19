package middleware

import (
	"context"
	"net/http"
)

type routeCtxKey struct{}

// WithRoutePattern looks up mux's own matched pattern for r via a pure
// mux.Handler(r) call — BEFORE any dispatch happens — and stores it in
// context for WithTracing/WithHTTPMetrics to read (docs/plan/43 K4/K5).
//
// This exists because reading r.Pattern AFTER calling next.ServeHTTP is
// NOT reliable once any middleware between the reader and the actual
// dispatch calls r.WithContext(...) (which both WithTracing and WithLogger
// do, to propagate the enriched logger) — WithContext returns a new
// *http.Request copy, and net/http's ServeMux sets Pattern on whatever
// copy it ultimately dispatches to, not the original pointer further up
// the chain retains. A middleware relying on "call next, then read
// r.Pattern" silently sees a stale/empty value the moment any context copy
// happens in between — caught only by tracing a real request through Tempo
// during docs/plan/43 Task T3 verification (payin-service's admin router
// reported route="unmatched" for a real, matched "/admin/payin/" request).
// mux.Handler(r) sidesteps this entirely: it's a pure lookup with no
// dependency on pointer identity or how many context-wrapping hops happen
// afterward.
//
// Limitation: this only resolves the pattern registered on THIS mux —
// if the matched handler is itself another *http.ServeMux (e.g. behind
// http.StripPrefix), a deeper/more specific pattern inside it is not
// visible here. Install WithRoutePattern for the mux closest to where
// WithTracing/WithHTTPMetrics run for the most specific label achievable
// without threading instrumentation into module-owned inner routers.
func WithRoutePattern(mux *http.ServeMux) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, pattern := mux.Handler(r)
			ctx := context.WithValue(r.Context(), routeCtxKey{}, pattern)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RouteFromCtx returns the pattern WithRoutePattern stored, or "" if this
// chain never installed it.
func RouteFromCtx(ctx context.Context) string {
	p, _ := ctx.Value(routeCtxKey{}).(string)
	return p
}

// effectiveRoutePattern prefers WithRoutePattern's pre-computed context
// value; when a chain never installs WithRoutePattern (the webhook chain in
// internal/handler/router.go wraps a single leaf handler already registered
// on the OUTER mux at an exact pattern, so the outer mux's own dispatch has
// already set r.Pattern correctly by the time this middleware runs — no
// context copy has happened yet at that point), r.Pattern is still a valid
// fallback.
func effectiveRoutePattern(r *http.Request) string {
	if p := RouteFromCtx(r.Context()); p != "" {
		return p
	}
	return r.Pattern
}
