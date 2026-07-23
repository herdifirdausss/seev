package handler

import (
	"net/http"

	"github.com/herdifirdausss/seev/pkg/response"
)

// healthStatus is the response body for health endpoints.
type healthStatus struct {
	Status     string            `json:"status"`
	Components map[string]string `json:"components"`
}

// Health handles GET /health — liveness probe.
// Returns 200 if the HTTP server is running, regardless of dependency state.
func Health(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]string{"status": "ok"})
}

// Ready handles GET /ready — readiness probe.
// Returns 200 only when all configured dependencies are healthy.
// Returns 503 with component details when any dependency is degraded.
func Ready(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		components := make(map[string]string)
		healthy := true

		if deps.DB != nil {
			if err := deps.DB.HealthCheck(r.Context()); err != nil {
				components["postgres"] = "unhealthy: " + err.Error()
				healthy = false
			} else {
				components["postgres"] = "ok"
			}
		}

		if deps.Cache != nil {
			if err := deps.Cache.HealthCheck(r.Context()); err != nil {
				components["redis"] = "unhealthy: " + err.Error()
				healthy = false
			} else {
				components["redis"] = "ok"
			}
		} else {
			// REDIS_ENABLED=false (docs/roadmap/archive/12 Task T1) — not a degraded
			// state, rate limiting and scheduler lock are running in-memory.
			components["redis"] = "disabled"
		}

		if deps.MQ != nil {
			if err := deps.MQ.HealthCheck(); err != nil {
				components["rabbitmq"] = "unhealthy: " + err.Error()
				healthy = false
			} else {
				components["rabbitmq"] = "ok"
			}
		}

		if deps.LedgerReady != nil {
			if err := deps.LedgerReady(r.Context()); err != nil {
				components["ledger"] = "unhealthy: " + err.Error()
				healthy = false
			} else {
				components["ledger"] = "ok"
			}
		}

		status := healthStatus{
			Status:     statusString(healthy),
			Components: components,
		}

		if !healthy {
			response.JSON(w, http.StatusServiceUnavailable, response.Envelope{
				Success: false,
				Data:    status,
			})
			return
		}

		response.OK(w, status)
	}
}

func statusString(healthy bool) string {
	if healthy {
		return "ok"
	}
	return "degraded"
}
