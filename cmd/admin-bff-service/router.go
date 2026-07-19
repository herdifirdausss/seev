package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/herdifirdausss/seev/internal/adminbff"
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
	root.Handle("/api/v1/admin/", module.AdminRouter())
	return middleware.Chain(
		middleware.WithRequestID(), middleware.WithRoutePattern(root), middleware.WithTracing(log),
		middleware.WithHTTPMetrics(), middleware.WithLogger(log), middleware.WithRecovery(),
		middleware.WithSecurityHeaders(middleware.DefaultSecurityHeadersConfig()),
		middleware.WithTimeout(30*time.Second),
	)(root)
}
