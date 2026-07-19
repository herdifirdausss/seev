// Package adminbff owns the operator-facing BFF.  It deliberately contains
// no domain persistence or business rules; those remain in the service that
// owns the downstream admin endpoint (docs/plan/47 K1/K2).
package adminbff

import (
	"encoding/json"
	"net/http"
)

// Module is the composition-root facade for admin-bff-service.  Later tasks
// add the session store, typed downstream clients, and templates behind this
// facade without exposing implementation packages to other services.
type Module struct{}

func NewModule() *Module { return &Module{} }

// AdminRouter is intentionally a safe placeholder until T2/T4 wire the
// authenticated proxy routes.  Returning 404 rather than exposing an
// unauthenticated mutation endpoint keeps the scaffold side-effect free.
func (m *Module) AdminRouter() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "admin route not found"})
	})
}
