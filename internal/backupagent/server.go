package backupagent

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewMux builds backup-agent's internal mTLS listener: /health, /ready,
// /metrics — the same three-endpoint shape every other internal listener
// in this repo exposes (cmd/assurance-service/main.go), plus a read-only
// /status endpoint for ad hoc operator inspection over the same
// authenticated channel the CLI `status` subcommand also uses.
func (a *Agent) NewMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		st, err := a.GetStatus(ctx)
		ready := err == nil && st.HasValidFullBackup && st.WithinRPOBudget
		w.Header().Set("Content-Type", "application/json")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			body := map[string]any{"status": "degraded"}
			if err != nil {
				body["error"] = err.Error()
			} else {
				body["has_valid_full_backup"] = st.HasValidFullBackup
				body["within_rpo_budget"] = st.WithinRPOBudget
				body["wal_archive_age_seconds"] = st.WALArchiveAgeSeconds
			}
			_ = json.NewEncoder(w).Encode(body)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ready"})
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		st, err := a.GetStatus(ctx)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(st)
	})
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}
