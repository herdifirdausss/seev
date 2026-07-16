package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

type adminHandlers interface{ AdminRouter() http.Handler }

func adminRouter(cfg *config.Config, handlers adminHandlers, log *slog.Logger) http.Handler {
	root := http.NewServeMux()
	root.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	root.Handle("GET /metrics", promhttp.Handler())
	authed := middleware.Chain(middleware.WithAuth(cfg.JWT.Secret, cfg.JWT.Issuer), middleware.RequireJSON())
	root.Handle("/api/v1/admin/fraud/", authed(handlers.AdminRouter()))
	return middleware.Chain(
		middleware.WithRequestID(), middleware.WithLogger(log), middleware.WithRecovery(),
		middleware.WithSecurityHeaders(middleware.DefaultSecurityHeadersConfig()), middleware.WithTimeout(30*time.Second),
	)(root)
}
