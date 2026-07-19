package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/herdifirdausss/seev/internal/adminbff"
	adminweb "github.com/herdifirdausss/seev/internal/adminbff/web"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

func adminRouter(cfg *config.Config, module *adminbff.Module, log *slog.Logger) http.Handler {
	root := http.NewServeMux()
	root.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	root.Handle("GET /metrics", promhttp.Handler())
	root.Handle("/assets/", http.StripPrefix("/assets/", adminweb.AssetHandler()))
	root.Handle("GET /login", module.LoginPage())
	root.Handle("POST /login", module.LoginHandler())
	root.Handle("POST /logout", module.RequireSession(module.RequireCSRF(module.LogoutHandler())))
	root.Handle("/api/v1/admin/", module.RequireSession(module.RequireCSRF(module.AdminRouter())))
	return middleware.Chain(
		middleware.WithRequestID(), middleware.WithRoutePattern(root), middleware.WithTracing(log),
		middleware.WithHTTPMetrics(), middleware.WithLogger(log), middleware.WithRecovery(),
		middleware.WithSecurityHeaders(middleware.DefaultSecurityHeadersConfig()),
		middleware.WithTimeout(30*time.Second),
	)(root)
}
