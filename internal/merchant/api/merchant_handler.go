package api

import (
	"net/http"

	"github.com/herdifirdausss/seev/internal/merchant/auth"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/pkg/response"
)

const opGetMerchant = "b2bGetMerchantV1"

type merchantProfileResponse struct {
	ID              string `json:"id"`
	ExternalCode    string `json:"external_code"`
	Name            string `json:"name"`
	Environment     string `json:"environment"`
	Status          string `json:"status"`
	DefaultCurrency string `json:"default_currency"`
}

// GetMerchantHandler implements GET /api/v1/b2b/merchant (b2bGetMerchantV1)
// — the authenticated tenant's own profile, read directly from Gateway's
// own Tenants repository. No owner-service call: unlike accounts/
// transactions/transfers, a tenant's own identity is edge-owned data
// (§3.1), not something Ledger has any opinion about.
func GetMerchantHandler(tenants repository.TenantRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			response.Unauthorized(w, "authentication required")
			return
		}
		tenant, err := tenants.GetByID(r.Context(), principal.TenantID)
		if err != nil {
			if err == repository.ErrNotFound {
				response.ErrorStatus(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found")
				return
			}
			response.InternalServerError(w, err)
			return
		}
		response.OK(w, merchantProfileResponse{
			ID: tenant.PublicID, ExternalCode: tenant.ExternalCode, Name: tenant.Name,
			Environment: tenant.Environment, Status: tenant.Status, DefaultCurrency: tenant.DefaultCurrency,
		})
	}
}
