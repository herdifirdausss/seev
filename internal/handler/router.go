package handler

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

// requireKYC gates a handler registered at an EXACT route pattern (POST
// /payout, POST /topup) — every request that reaches it already IS the
// route being gated, so it enforces min unconditionally. Use
// requireKYCForLedgerPostings instead for a handler (like the ledger
// reverse proxy) that serves multiple sub-paths and needs its own
// method/path carve-out.
func requireKYC(min int) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := middleware.GetClaims(r.Context())
			if claims == nil || claims.KYCLevel < min {
				response.JSON(w, http.StatusForbidden, map[string]any{"code": "KYC_REQUIRED", "min_level": min})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireKYCForLedgerPostings wraps the ledger reverse proxy, which serves
// GET and POST across many sub-paths (accounts, statements, fees/quote,
// transactions, ...) — unlike requireKYC above, it must itself distinguish
// which request to gate: only POST against /transactions* (the only
// sub-path that moves money). GET and POST /fees/quote stay reachable at
// L0 (docs/plan/39 Task T4 — a quote never moves money).
func requireKYCForLedgerPostings(min int) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/api/v1/ledger/transactions") {
				next.ServeHTTP(w, r)
				return
			}
			claims := middleware.GetClaims(r.Context())
			if claims == nil || claims.KYCLevel < min {
				response.JSON(w, http.StatusForbidden, map[string]any{"code": "KYC_REQUIRED", "min_level": min})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// NewRouter registers all public-facing routes with their middleware
// chains. Uses Go 1.22 enhanced net/http patterns — no third-party router
// required. See NewInternalRouter for the network-isolated counterpart that
// serves system-transaction types, /metrics, and admin tooling
// (docs/plan/10 Task T1).
func NewRouter(cfg *config.Config, deps *Dependencies, logger *slog.Logger) http.Handler {
	limiter := buildRateLimiter(deps)
	root := http.NewServeMux()
	apiRoot := http.NewServeMux()

	// ─── Infrastructure probes (NO middleware) ───────────────────────────────
	root.HandleFunc("GET /health", Health)
	root.HandleFunc("GET /ready", Ready(deps))

	// ─── Global middleware ────────────────────────────────────────────────────
	global := middleware.Chain(
		middleware.WithRequestID(),
		middleware.WithLogger(logger),
		middleware.WithRecovery(),
		middleware.WithSecurityHeaders(securityHeadersConfig(cfg)),
		middleware.WithCORS(corsConfig(cfg)),
		middleware.WithTimeout(30*time.Second),
	)

	// ─── Vendor webhooks (docs/plan/22 Task T3, decision K-T1) ───────────────
	// Deliberately its OWN chain, not `global`: no CORS (a payment vendor's
	// server-to-server POST is never a browser request — a CORS preflight
	// would only reject it), no JWT/RequireJSON (the vendor authenticates
	// via a per-vendor signature verified inside the handler, not this
	// app's own auth), but still rate-limited (per-vendor key, not per-IP —
	// see middleware.RateLimitByVendor) and still gets request
	// ID/logging/recovery/security-headers/timeout like everything else.
	// Mounted directly on `root`, never under /api/v1 — a vendor's webhook
	// URL is a stable top-level path.
	webhookChain := middleware.Chain(
		middleware.WithRequestID(),
		middleware.WithLogger(logger),
		middleware.WithRecovery(),
		middleware.WithSecurityHeaders(securityHeadersConfig(cfg)),
		middleware.WithRateLimit(limiter, middleware.RateLimitByVendor),
		middleware.WithTimeout(30*time.Second),
	)
	root.Handle("POST /webhooks/{vendor}", webhookChain(webhookHandler(deps, logger)))

	// ─── API v1 ───────────────────────────────────────────────────────────────
	apiMux := http.NewServeMux()

	// Authenticated
	authed := middleware.Chain(
		middleware.WithRateLimit(limiter, middleware.RateLimitByIPAndPath),
		middleware.WithAuth(cfg.JWT.Secret, cfg.JWT.Issuer),
		middleware.RequireJSON(),
	)

	// Preserve the complete inbound path: ledger-service owns the same
	// /api/v1/ledger/* surface, so the proxy receives the request before the
	// local /api/v1 prefix is stripped for monolith-owned routes.
	if deps.LedgerProxy != nil {
		root.Handle("/api/v1/ledger/", global(authed(requireKYCForLedgerPostings(1)(deps.LedgerProxy))))
	}

	// Payout module — user-facing create/get (docs/plan/23 Task T5).
	// Registered directly at their literal final paths (not nested behind
	// a StripPrefix sub-router) since there are only two routes and one of
	// them is the bare "/payout" path itself — nesting a nil-vs-set
	// distinction there is not worth the added net/http subtree-redirect
	// subtlety a "POST /" pattern under a stripped prefix would introduce.
	if deps.Payout != nil {
		apiMux.Handle("POST /payout", authed(requireKYC(1)(createPayoutHandler(deps.Payout))))
		apiMux.Handle("GET /payout/{id}", authed(getPayoutHandler(deps.Payout)))
	}

	// Payin topup intents — user-facing create/get (docs/plan/25 Task T3),
	// same direct-registration pattern as Payout above.
	if deps.Payin != nil {
		apiMux.Handle("POST /topup", authed(requireKYC(1)(createTopupIntentHandler(deps.Payin))))
		apiMux.Handle("GET /topup/{id}", authed(getTopupIntentHandler(deps.Payin)))
	}

	// Notify — in-app notification inbox (docs/plan/25 Task T4), same
	// direct-registration pattern as Payout/Payin above.
	if deps.Notify != nil {
		apiMux.Handle("GET /notifications", authed(deps.Notify.ListHandler()))
		apiMux.Handle("POST /notifications/{id}/read", authed(deps.Notify.MarkReadHandler()))
	}

	apiRoot.Handle("/api/v1/", http.StripPrefix("/api/v1", apiMux))

	// Catch-all inside global
	apiRoot.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	// Mount API with middleware
	root.Handle("/", global(apiRoot))

	return root
}

// NewInternalRouter registers the routes meant only for the internal-only
// listener (INTERNAL_APP_PORT, bound to 127.0.0.1 by default — see
// cmd/gateway/main.go). It carries every ledger transaction type (including
// money_in, refund, withdraw settlement, escrow release, fee_collect —
// never safe for direct end-user use), /metrics, and admin outbox tooling.
// Auth is still required; there is no rate limiting here because the caller
// is assumed to be a trusted internal service, not a public client
// (docs/plan/10 Task T1).
func NewInternalRouter(cfg *config.Config, deps *Dependencies, logger *slog.Logger) http.Handler {
	root := http.NewServeMux()
	apiRoot := http.NewServeMux()

	// NOTE: /metrics now lives ONLY on the internal listener — it is never
	// reachable from the public-facing port (docs/plan/10 Task T6).
	root.Handle("GET /metrics", promhttp.Handler())

	global := middleware.Chain(
		middleware.WithRequestID(),
		middleware.WithLogger(logger),
		middleware.WithRecovery(),
		middleware.WithSecurityHeaders(securityHeadersConfig(cfg)),
		middleware.WithTimeout(30*time.Second),
	)

	apiMux := http.NewServeMux()

	apiRoot.Handle("/api/v1/", http.StripPrefix("/api/v1", apiMux))
	apiRoot.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	root.Handle("/", global(apiRoot))

	return root
}

// buildRateLimiter returns a Redis-backed limiter, or an in-memory fallback
// when Redis is disabled/unavailable (docs/plan/12 Task T1).
func buildRateLimiter(deps *Dependencies) cache.Limiter {
	rateCfg := cache.RateConfig{Requests: 10, Per: 1 * time.Minute, Burst: 10}
	if deps.Cache != nil {
		return cache.NewRedisRateLimiter(deps.Cache.Redis(), rateCfg)
	}
	return cache.NewMemoryRateLimiter(rateCfg)
}

func corsConfig(cfg *config.Config) middleware.CORSConfig {
	corsCfg := middleware.DefaultCORSConfig()
	if cfg.IsProduction() {
		corsCfg.AllowedOrigins = []string{cfg.App.BaseURL}
		corsCfg.AllowCredentials = true
	}
	return corsCfg
}

func securityHeadersConfig(cfg *config.Config) middleware.SecurityHeadersConfig {
	secCfg := middleware.DefaultSecurityHeadersConfig()
	secCfg.TrustProxyHeaders = cfg.App.TrustProxyHeaders
	return secCfg
}
