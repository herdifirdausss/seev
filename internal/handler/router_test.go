package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

func testConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{
			Env:     "development",
			BaseURL: "http://localhost:8080",
		},
		JWT: config.JWTConfig{
			Secret:       "supersecretkeythatisatleast32chars!",
			AccessExpiry: 15 * time.Minute,
		},
	}
}

func newTestRouter(cfg *config.Config) http.Handler {
	deps := &Dependencies{
		Cache: &cache.MockCache{
			RedisFn: func() *redis.Client {
				return redis.NewClient(&redis.Options{
					Addr: "localhost:6380",
				})
			},
		},
	}
	return NewRouter(cfg, deps, slog.Default())
}

func doRequest(t *testing.T, handler http.Handler, method, path string, extraHeaders ...map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for _, headers := range extraHeaders {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// ─── Infrastructure routes ────────────────────────────────────────────────────

func TestRouter_HealthEndpoint(t *testing.T) {
	router := newTestRouter(testConfig())
	w := doRequest(t, router, http.MethodGet, "/health")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRouter_ReadyEndpoint(t *testing.T) {
	router := newTestRouter(testConfig())
	w := doRequest(t, router, http.MethodGet, "/ready")
	// With only mock cache (no db/mq), should be OK
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRouter_ReadyEndpoint_LedgerDown(t *testing.T) {
	deps := &Dependencies{LedgerReady: func(context.Context) error { return errors.New("unavailable") }}
	router := NewRouter(testConfig(), deps, slog.Default())
	w := doRequest(t, router, http.MethodGet, "/ready")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "ledger")
}

// ─── Public API routes ────────────────────────────────────────────────────────

// TestRouter_PublicAuthRoutes_404WithoutModule: since docs/plan/25 Task T1
// the auth routes are real handlers on deps.Auth (the 501 placeholders are
// gone). A router built WITHOUT an auth module must 404 those paths — the
// same nil-guard contract every other module uses.
func TestRouter_PublicAuthRoutes_404WithoutModule(t *testing.T) {
	router := newTestRouter(testConfig())
	routes := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/refresh"},
		{http.MethodPost, "/api/v1/auth/register"},
	}
	for _, rt := range routes {
		t.Run(rt.path, func(t *testing.T) {
			w := doRequest(t, router, rt.method, rt.path)
			assert.Equal(t, http.StatusNotFound, w.Code)
		})
	}
}

// ─── Authenticated routes ─────────────────────────────────────────────────────

func validToken(t *testing.T) string {
	t.Helper()
	token, err := middleware.GenerateToken(
		"supersecretkeythatisatleast32chars!",
		middleware.Claims{
			UserID:   "u1",
			Role:     "user",
			KYCLevel: 1,
			Exp:      9999999999,
		},
	)
	assert.NoError(t, err)
	return token
}

// Since docs/plan/25 Task T1, /users/me belongs to the auth module and is
// nil-guarded like every other module route: without deps.Auth wired the
// path simply doesn't exist (404), token or not. The 401-without-token
// contract for authed chains is covered by the module-level tests that wire
// real handlers behind real WithAuth (e.g. internal/payout/http_test.go
// TestCreateHandler_NoToken_401, internal/auth integration tests).
func TestRouter_UsersMe_404WithoutAuthModule(t *testing.T) {
	router := newTestRouter(testConfig())

	w := doRequest(t, router, http.MethodGet, "/api/v1/users/me")
	assert.Equal(t, http.StatusNotFound, w.Code)

	token := validToken(t)
	w = doRequest(t, router, http.MethodGet, "/api/v1/users/me",
		map[string]string{"Authorization": "Bearer " + token},
	)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRouter_RemovedAdminUsersRoute_Returns404(t *testing.T) {
	router := newTestRouter(testConfig())
	w := doRequest(t, router, http.MethodGet, "/api/v1/admin/users")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRouter_LedgerProxy_PreservesPathAndQuery(t *testing.T) {
	seen := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.RequestURI()
		w.WriteHeader(http.StatusCreated)
	}))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	assert.NoError(t, err)
	deps := &Dependencies{LedgerProxy: httputil.NewSingleHostReverseProxy(target)}
	router := NewRouter(testConfig(), deps, slog.Default())
	token := validToken(t)
	w := doRequest(t, router, http.MethodGet, "/api/v1/ledger/accounts?currency=IDR",
		map[string]string{"Authorization": "Bearer " + token},
	)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "/api/v1/ledger/accounts?currency=IDR", <-seen)
}

// ─── KYC gating (docs/plan/39 Task T4) ───────────────────────────────────────

func tokenWithKYCLevel(t *testing.T, level int) string {
	t.Helper()
	token, err := middleware.GenerateToken(
		"supersecretkeythatisatleast32chars!",
		middleware.Claims{UserID: "u1", Role: "user", KYCLevel: level, Exp: 9999999999},
	)
	assert.NoError(t, err)
	return token
}

// TestRequireKYCForLedgerPostings_GatesOnlyPostTransactions proves the
// ledger-proxy carve-out exactly: POST /transactions* is gated (L0 -> 403,
// L1 passes), while GET and POST /fees/quote reach the backend at L0 — a
// quote never moves money.
func TestRequireKYCForLedgerPostings_GatesOnlyPostTransactions(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	assert.NoError(t, err)
	deps := &Dependencies{LedgerProxy: httputil.NewSingleHostReverseProxy(target)}
	router := NewRouter(testConfig(), deps, slog.Default())

	l0 := tokenWithKYCLevel(t, 0)
	l1 := tokenWithKYCLevel(t, 1)

	w := doRequest(t, router, http.MethodPost, "/api/v1/ledger/transactions",
		map[string]string{"Authorization": "Bearer " + l0, "Content-Type": "application/json"})
	assert.Equal(t, http.StatusForbidden, w.Code, "L0 posting a transaction must be rejected")
	assert.Contains(t, w.Body.String(), "KYC_REQUIRED")

	w = doRequest(t, router, http.MethodPost, "/api/v1/ledger/transactions",
		map[string]string{"Authorization": "Bearer " + l1, "Content-Type": "application/json"})
	assert.Equal(t, http.StatusCreated, w.Code, "L1 posting a transaction must reach the backend")

	w = doRequest(t, router, http.MethodGet, "/api/v1/ledger/accounts",
		map[string]string{"Authorization": "Bearer " + l0})
	assert.Equal(t, http.StatusCreated, w.Code, "GET must pass through at L0")

	w = doRequest(t, router, http.MethodPost, "/api/v1/ledger/fees/quote",
		map[string]string{"Authorization": "Bearer " + l0, "Content-Type": "application/json"})
	assert.Equal(t, http.StatusCreated, w.Code, "POST /fees/quote must pass through at L0 — a quote never moves money")
}

// TestRequireKYC_ExactRouteMount_GatesUnconditionally is the regression test
// for the bug this task's own missing coverage let through: requireKYC
// (used verbatim at POST /payout and POST /topup — exact-match routes
// reached via http.StripPrefix, so r.URL.Path is never under
// /api/v1/ledger/transactions) must gate on claims alone, not on a path
// check that can never match at those mount points.
func TestRequireKYC_ExactRouteMount_GatesUnconditionally(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	chain := middleware.Chain(middleware.WithAuth(testConfig().JWT.Secret, ""), requireKYC(1))(inner)

	l0 := tokenWithKYCLevel(t, 0)
	l1 := tokenWithKYCLevel(t, 1)

	for _, path := range []string{"/payout", "/topup"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer "+l0)
		w := httptest.NewRecorder()
		chain.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code, "L0 at exact-match route %s must be rejected", path)
		assert.Contains(t, w.Body.String(), "KYC_REQUIRED")

		req2 := httptest.NewRequest(http.MethodPost, path, nil)
		req2.Header.Set("Authorization", "Bearer "+l1)
		w2 := httptest.NewRecorder()
		chain.ServeHTTP(w2, req2)
		assert.Equal(t, http.StatusOK, w2.Code, "L1 at exact-match route %s must pass", path)
	}
}

// ─── Unknown routes ───────────────────────────────────────────────────────────

func TestRouter_UnknownRoute_Returns404(t *testing.T) {
	router := newTestRouter(testConfig())
	w := doRequest(t, router, http.MethodGet, "/this/does/not/exist")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ─── Security headers ─────────────────────────────────────────────────────────

func TestRouter_SecurityHeadersPresent(t *testing.T) {
	router := newTestRouter(testConfig())
	w := doRequest(t, router, http.MethodGet, "/random")
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.NotEmpty(t, w.Header().Get("X-Request-Id"))
}

// ─── Production CORS config ───────────────────────────────────────────────────

func TestCORSConfig_Development(t *testing.T) {
	cfg := testConfig()
	cors := corsConfig(cfg)
	assert.Contains(t, cors.AllowedOrigins, "*")
}

func TestCORSConfig_Production(t *testing.T) {
	cfg := testConfig()
	cfg.App.Env = "production"
	cfg.App.BaseURL = "https://production.example.com"

	cors := corsConfig(cfg)
	assert.Equal(t, []string{"https://production.example.com"}, cors.AllowedOrigins)
	assert.True(t, cors.AllowCredentials)
}

// ─── Internal router (docs/plan/10 Task T1) ────────────────────────────────────

func newTestInternalRouter(cfg *config.Config) http.Handler {
	deps := &Dependencies{
		Cache: &cache.MockCache{
			RedisFn: func() *redis.Client {
				return redis.NewClient(&redis.Options{Addr: "localhost:6380"})
			},
		},
	}
	return NewInternalRouter(cfg, deps, slog.Default())
}

func TestRouter_Metrics_NotReachableOnPublicRouter(t *testing.T) {
	router := newTestRouter(testConfig())
	w := doRequest(t, router, http.MethodGet, "/metrics")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestInternalRouter_MetricsEndpoint(t *testing.T) {
	router := newTestInternalRouter(testConfig())
	w := doRequest(t, router, http.MethodGet, "/metrics")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestInternalRouter_UnknownRoute_Returns404(t *testing.T) {
	// Internal ledger/policy surfaces moved to ledger-service in phase 6b.
	router := newTestInternalRouter(testConfig())
	w := doRequest(t, router, http.MethodPost, "/api/v1/ledger/transactions")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ─── Redis-optional rate limiter fallback (docs/plan/12 Task T1) ──────────────

func TestRouter_NilCache_FallsBackToMemoryLimiter_StillServesRequests(t *testing.T) {
	deps := &Dependencies{Cache: nil}
	router := NewRouter(testConfig(), deps, slog.Default())

	w := doRequest(t, router, http.MethodGet, "/health")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestBuildRateLimiter_NilCache_ReturnsMemoryLimiter(t *testing.T) {
	limiter := buildRateLimiter(&Dependencies{Cache: nil})
	_, ok := limiter.(*cache.MemoryRateLimiter)
	assert.True(t, ok, "expected *cache.MemoryRateLimiter when deps.Cache is nil")
}

func TestBuildRateLimiter_WithCache_ReturnsRedisLimiter(t *testing.T) {
	deps := &Dependencies{
		Cache: &cache.MockCache{
			RedisFn: func() *redis.Client {
				return redis.NewClient(&redis.Options{Addr: "localhost:6380"})
			},
		},
	}
	limiter := buildRateLimiter(deps)
	_, ok := limiter.(*cache.RedisRateLimiter)
	assert.True(t, ok, "expected *cache.RedisRateLimiter when deps.Cache is set")
}
