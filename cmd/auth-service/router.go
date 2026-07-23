package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

type authHandlers interface {
	RegisterHandler() http.HandlerFunc
	LoginHandler() http.HandlerFunc
	RefreshHandler() http.HandlerFunc
	MeHandler() http.HandlerFunc
	UpdateMeHandler() http.HandlerFunc
}

type kycHandlers interface {
	SubmitKYCHandler() http.HandlerFunc
	KYCStatusHandler() http.HandlerFunc
	UploadKYCDocumentHandler() http.HandlerFunc
	AdminListKYCHandler() http.HandlerFunc
	AdminApproveKYCHandler() http.HandlerFunc
	AdminRejectKYCHandler() http.HandlerFunc
	AdminDowngradeKYCHandler() http.HandlerFunc
	AdminDownloadKYCDocumentHandler() http.HandlerFunc
}

func publicRouter(cfg *config.Config, handlers authHandlers, redisCache *cache.Cache, log *slog.Logger) http.Handler {
	root := http.NewServeMux()
	apiRoot := http.NewServeMux()
	api := http.NewServeMux()

	limiterConfig := cache.RateConfig{Requests: cfg.App.RateLimitRequests, Per: cfg.App.RateLimitPer, Burst: cfg.App.RateLimitBurst}
	var limiter cache.Limiter
	if redisCache != nil {
		// docs/roadmap/archive/45 Task T3/K4: fails over to an in-memory limiter at
		// runtime if Redis becomes unreachable, recovering automatically.
		limiter = cache.NewFailoverLimiter(redisCache.Redis(), limiterConfig, log)
	} else {
		limiter = cache.NewMemoryRateLimiter(limiterConfig)
	}

	global := middleware.Chain(
		middleware.WithRequestID(),
		middleware.WithRoutePattern(apiRoot),
		middleware.WithTracing(log), middleware.WithHTTPMetrics(),
		middleware.WithLogger(log),
		middleware.WithRecovery(),
		middleware.WithSecurityHeaders(authSecurityHeaders(cfg)),
		middleware.WithCORS(authCORS(cfg)),
		middleware.WithTimeout(30*time.Second),
	)
	public := middleware.Chain(
		middleware.WithRateLimit(limiter, middleware.RateLimitByIPAndPath),
		middleware.RequireJSON(),
	)
	authed := middleware.Chain(
		middleware.WithRateLimit(limiter, middleware.RateLimitByIPAndPath),
		middleware.WithAuth(cfg.JWT.Secret, cfg.JWT.Issuer),
		middleware.RequireJSON(),
	)

	api.Handle("POST /auth/register", public(handlers.RegisterHandler()))
	api.Handle("POST /auth/login", public(handlers.LoginHandler()))
	api.Handle("POST /auth/refresh", public(handlers.RefreshHandler()))
	api.Handle("GET /users/me", authed(handlers.MeHandler()))
	api.Handle("PUT /users/me", authed(handlers.UpdateMeHandler()))
	if kyc, ok := handlers.(kycHandlers); ok {
		api.Handle("POST /users/me/kyc", authed(kyc.SubmitKYCHandler()))
		api.Handle("GET /users/me/kyc", authed(kyc.KYCStatusHandler()))
		api.Handle("POST /users/me/kyc/documents", authed(kyc.UploadKYCDocumentHandler()))
	}
	apiRoot.Handle("/api/v1/", http.StripPrefix("/api/v1", api))
	apiRoot.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	root.Handle("/", global(apiRoot))
	return root
}

func internalRouter(args ...any) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.Handle("GET /metrics", promhttp.Handler())
	if len(args) >= 2 {
		cfg, cfgOK := args[0].(*config.Config)
		handlers, handlersOK := args[1].(kycHandlers)
		if cfgOK && handlersOK {
			authedAdmin := middleware.Chain(middleware.WithAuth(cfg.JWT.Secret, cfg.JWT.Issuer), middleware.WithRole("admin", "admin_maker", "admin_checker"), middleware.RequireJSON())
			mux.Handle("GET /api/v1/admin/kyc/submissions", authedAdmin(handlers.AdminListKYCHandler()))
			mux.Handle("POST /api/v1/admin/kyc/submissions/{id}/approve", authedAdmin(handlers.AdminApproveKYCHandler()))
			mux.Handle("POST /api/v1/admin/kyc/submissions/{id}/reject", authedAdmin(handlers.AdminRejectKYCHandler()))
			mux.Handle("POST /api/v1/admin/kyc/users/{id}/downgrade", authedAdmin(handlers.AdminDowngradeKYCHandler()))
			mux.Handle("GET /api/v1/admin/kyc/documents/{id}", authedAdmin(handlers.AdminDownloadKYCDocumentHandler()))
		}
	}
	return mux
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func authCORS(cfg *config.Config) middleware.CORSConfig {
	cors := middleware.DefaultCORSConfig()
	switch {
	case cfg.IsProduction():
		cors.AllowedOrigins = []string{cfg.App.BaseURL}
		cors.AllowCredentials = true
	case len(cfg.App.AllowedOrigins) > 0:
		cors.AllowedOrigins = cfg.App.AllowedOrigins
	}
	return cors
}

func authSecurityHeaders(cfg *config.Config) middleware.SecurityHeadersConfig {
	security := middleware.DefaultSecurityHeadersConfig()
	security.TrustProxyHeaders = cfg.App.TrustProxyHeaders
	return security
}
