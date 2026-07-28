package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/httpcontract"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

type adminHandlers interface{ AdminRouter() http.Handler }

// privacyHandlers is docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T4b/T5b's own
// export+closure route set — same optional-type-assertion convention as
// every other handler interface in this codebase.
type privacyHandlers interface{ PrivacyRouter() http.Handler }

func adminRouter(cfg *config.Config, handlers adminHandlers, log *slog.Logger) http.Handler {
	root := httpcontract.New(httpcontract.Options{Owner: "payin", Audience: "operational", Contract: "internal-v1"})
	root.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	root.Handle("GET /metrics", promhttp.Handler())
	authed := middleware.Chain(middleware.WithAuth(cfg.JWT.Secret, cfg.JWT.Issuer), middleware.RequireJSON())
	root.Handle("/admin/payin/", authed(handlers.AdminRouter()))
	// docs/roadmap/archive/51 T4b/T5b: called by auth-service's own saga/export
	// workers, never by an end-user JWT — gated by the shared internal
	// token (not `authed`'s JWT check) plus this listener's own mTLS
	// identity allowlist (cmd/payin-service/main.go gains tlsx.IdentityAuth).
	if privacy, ok := handlers.(privacyHandlers); ok {
		root.Handle("/privacy/", middleware.WithInternalToken(cfg.InternalGRPCToken)(privacy.PrivacyRouter()))
	}
	return middleware.Chain(middleware.WithRequestID(), middleware.WithRoutePattern(root), middleware.WithTracing(log), middleware.WithHTTPMetrics(), middleware.WithLogger(log), middleware.WithRecovery(), middleware.WithSecurityHeaders(middleware.DefaultSecurityHeadersConfig()), middleware.WithTimeout(30*time.Second))(root)
}
