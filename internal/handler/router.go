package handler

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/httpcontract"
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
// L0 (docs/roadmap/archive/39 Task T4 — a quote never moves money).
func requireKYCForLedgerPostings(min int) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost ||
				(!strings.HasPrefix(r.URL.Path, "/api/v1/ledger/transactions") &&
					!strings.HasPrefix(r.URL.Path, "/api/v1/ledger/fx/conversions") &&
					!strings.HasPrefix(r.URL.Path, "/api/v1/fx/conversions")) {
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
// (docs/roadmap/archive/10 Task T1).
func NewRouter(cfg *config.Config, deps *Dependencies, logger *slog.Logger) http.Handler {
	limiter := buildRateLimiter(cfg, deps, logger)
	root := httpcontract.New(httpcontract.Options{Owner: "gateway", Audience: "operational", Contract: "public-v1"})
	apiRoot := httpcontract.New(httpcontract.Options{Owner: "gateway", Audience: "public", Contract: "public-v1"})

	// ─── Infrastructure probes (NO middleware) ───────────────────────────────
	root.HandleFunc("GET /health", Health)
	root.HandleFunc("GET /ready", Ready(deps))

	// ─── Global middleware ────────────────────────────────────────────────────
	global := middleware.Chain(
		middleware.WithRequestID(),
		middleware.WithRoutePattern(apiRoot),
		middleware.WithTracing(logger), middleware.WithHTTPMetrics(),
		middleware.WithLogger(logger),
		middleware.WithRecovery(),
		middleware.WithSecurityHeaders(securityHeadersConfig(cfg)),
		middleware.WithCORS(corsConfig(cfg)),
		middleware.WithTimeout(30*time.Second),
	)

	// Vendor callbacks are no longer accepted by Gateway. They terminate at
	// VendorService's restricted callback edge, which verifies, persists, and
	// delivers only normalized owner callbacks.

	// ─── API v1 ───────────────────────────────────────────────────────────────
	apiMux := httpcontract.New(httpcontract.Options{Owner: "gateway", Audience: "public", Contract: "public-v1"})

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
		// C4 keeps Ledger as the sole wallet/FX owner while exposing the
		// product-facing paths without the internal owner prefix. The alias
		// rewrites only the downstream path; auth, request IDs, tracing, and
		// KYC middleware remain identical to /api/v1/ledger/*.
		ledgerAlias := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originalPath, originalRawPath := r.URL.Path, r.URL.RawPath
				r.URL.Path = "/api/v1/ledger" + strings.TrimPrefix(originalPath, "/api/v1")
				r.URL.RawPath = ""
				next.ServeHTTP(w, r)
				r.URL.Path, r.URL.RawPath = originalPath, originalRawPath
			})
		}
		walletFX := global(authed(requireKYCForLedgerPostings(1)(ledgerAlias(deps.LedgerProxy))))
		root.Handle("/api/v1/currencies", walletFX)
		root.Handle("/api/v1/currencies/", walletFX)
		root.Handle("/api/v1/balances", walletFX)
		root.Handle("/api/v1/balances/", walletFX)
		root.Handle("/api/v1/fx/", walletFX)
	}

	// Payout module — user-facing create/get (docs/roadmap/archive/23 Task T5).
	// Registered directly at their literal final paths (not nested behind
	// a StripPrefix sub-router) since there are only two routes and one of
	// them is the bare "/payout" path itself — nesting a nil-vs-set
	// distinction there is not worth the added net/http subtree-redirect
	// subtlety a "POST /" pattern under a stripped prefix would introduce.
	if deps.Payout != nil {
		apiMux.Handle("POST /payout", authed(requireKYC(1)(createPayoutHandler(deps.Payout))))
		apiMux.Handle("GET /payout/{id}", authed(getPayoutHandler(deps.Payout)))
	}

	// Payin topup intents — user-facing create/get (docs/roadmap/archive/25 Task T3),
	// same direct-registration pattern as Payout above.
	if deps.Payin != nil {
		apiMux.Handle("POST /topup", authed(requireKYC(1)(createTopupIntentHandler(deps.Payin))))
		apiMux.Handle("GET /topup/{id}", authed(getTopupIntentHandler(deps.Payin)))
	}

	// Notify — in-app notification inbox (docs/roadmap/archive/25 Task T4), same
	// direct-registration pattern as Payout/Payin above.
	if deps.Notify != nil {
		apiMux.Handle("GET /notifications", authed(deps.Notify.ListHandler()))
		apiMux.Handle("POST /notifications/{id}/read", authed(deps.Notify.MarkReadHandler()))
	}

	// Merchant/B2B API (Plan 57, roadmap track C1) — mounted UNAUTHENTICATED
	// by this router's own `authed` (JWT) chain: B2B principals are machine
	// API keys, never AuthService users (§3.2), so internal/merchant/api's
	// own router applies T3's RequireMerchantAuth/RequireScope and T4's
	// RequireQuota per route instead. Still runs inside the shared `global`
	// chain below (request id, tracing, metrics, recovery, security
	// headers, timeout) exactly like every other apiMux route.
	if deps.B2B != nil {
		apiMux.Handle("/b2b/", http.StripPrefix("/b2b", deps.B2B))
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
// (docs/roadmap/archive/10 Task T1).
func NewInternalRouter(cfg *config.Config, deps *Dependencies, logger *slog.Logger) http.Handler {
	root := httpcontract.New(httpcontract.Options{Owner: "gateway", Audience: "operational", Contract: "internal-v1"})
	apiRoot := httpcontract.New(httpcontract.Options{Owner: "gateway", Audience: "internal", Contract: "internal-v1"})

	// NOTE: /metrics now lives ONLY on the internal listener — it is never
	// reachable from the public-facing port (docs/roadmap/archive/10 Task T6).
	root.Handle("GET /metrics", promhttp.Handler())

	// docs/roadmap/archive/51 T4b/T5b (K9/K10/K11): auth-service's own export/closure
	// saga calls gateway's notif_notifications owner contract here — same
	// bare "/privacy/..." path every other owner mounts at (the client is
	// one generic type, internal/auth.httpOwnerClosureClient, that assumes
	// an identical relative path across every owner), gated by the shared
	// internal token rather than an end-user JWT (this listener has no
	// JWT-`authed` chain to mount alongside, unlike payin/payout/fraud).
	if deps.Notify != nil {
		root.Handle("/privacy/", middleware.WithInternalToken(cfg.InternalGRPCToken)(deps.Notify.PrivacyRouter()))
	}

	global := middleware.Chain(
		middleware.WithRequestID(),
		middleware.WithRoutePattern(apiRoot),
		middleware.WithTracing(logger), middleware.WithHTTPMetrics(),
		middleware.WithLogger(logger),
		middleware.WithRecovery(),
		middleware.WithSecurityHeaders(securityHeadersConfig(cfg)),
		middleware.WithTimeout(30*time.Second),
	)

	apiMux := httpcontract.New(httpcontract.Options{Owner: "gateway", Audience: "internal", Contract: "internal-v1"})

	// Plan 57 T8: Admin BFF's own generic proxy already targets
	// /api/v1/admin/gateway/ (internal/adminbff/module.go) — mount
	// internal/merchant.Module's AdminRouter() here, gated by the same
	// JWT `authed` chain ledger/payin/payout already use for their own
	// admin routes (this listener had no JWT chain at all before this;
	// every other route on it authenticates via mTLS identity alone).
	if deps.Merchant != nil {
		authed := middleware.Chain(middleware.WithAuth(cfg.JWT.Secret, cfg.JWT.Issuer), middleware.RequireJSON())
		// apiMux is itself mounted at apiRoot's "/api/v1/" with that prefix
		// already stripped (below), so AdminRouter()'s own routes — already
		// registered at the full "/admin/gateway/..." pattern — are reached
		// unmodified from here; no further StripPrefix needed.
		apiMux.Handle("/admin/gateway/", authed(deps.Merchant.AdminRouter()))
	}

	apiRoot.Handle("/api/v1/", http.StripPrefix("/api/v1", apiMux))
	apiRoot.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	root.Handle("/", global(apiRoot))

	return root
}

// buildRateLimiter returns a Redis-backed limiter that fails over to an
// in-memory fallback at RUNTIME if Redis becomes unreachable (docs/roadmap/archive/45
// Task T3/K4), recovering automatically once Redis is healthy again for
// two consecutive background probes — or a plain in-memory limiter with no
// failover machinery at all when Redis is disabled/unavailable at startup
// (docs/roadmap/archive/12 Task T1's original behavior, unchanged for that case: there
// is nothing to fail over TO).
func buildRateLimiter(cfg *config.Config, deps *Dependencies, logger *slog.Logger) cache.Limiter {
	rateCfg := cache.RateConfig{Requests: cfg.App.RateLimitRequests, Per: cfg.App.RateLimitPer, Burst: cfg.App.RateLimitBurst}
	if deps.Cache != nil {
		return cache.NewFailoverLimiter(deps.Cache.Redis(), rateCfg, logger)
	}
	return cache.NewMemoryRateLimiter(rateCfg)
}

func corsConfig(cfg *config.Config) middleware.CORSConfig {
	corsCfg := middleware.DefaultCORSConfig()
	switch {
	case cfg.IsProduction():
		corsCfg.AllowedOrigins = []string{cfg.App.BaseURL}
		corsCfg.AllowCredentials = true
	case len(cfg.App.AllowedOrigins) > 0:
		corsCfg.AllowedOrigins = cfg.App.AllowedOrigins
	}
	return corsCfg
}

func securityHeadersConfig(cfg *config.Config) middleware.SecurityHeadersConfig {
	secCfg := middleware.DefaultSecurityHeadersConfig()
	secCfg.TrustProxyHeaders = cfg.App.TrustProxyHeaders
	return secCfg
}
