package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/merchant/auth"
	"github.com/herdifirdausss/seev/internal/merchant/client"
	"github.com/herdifirdausss/seev/internal/merchant/idempotency"
	"github.com/herdifirdausss/seev/pkg/response"
)

// Operation IDs (api/openapi/b2b-v1.yaml) — also the keys registered in
// internal/merchant/auth's scope registry and the idempotency canonical
// hash's own operation component (§10.2), so these three things can never
// silently drift apart from a typo in one place.
const (
	opCreatePayin = "b2bCreatePayinV1"
	opGetPayin    = "b2bGetPayinV1"
)

type createPayinRequest struct {
	Amount      string         `json:"amount"`
	Currency    string         `json:"currency"`
	ReferenceID string         `json:"reference_id,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type payinResponse struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Amount    string    `json:"amount"`
	Currency  string    `json:"currency"`
	Vendor    string    `json:"vendor"`
	Livemode  bool      `json:"livemode"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func payinResponseFromResult(result client.PayinResult, livemode bool) payinResponse {
	return payinResponse{
		ID: result.ID.String(), Status: mapPayinStatus(result.Status), Amount: result.Amount, Currency: result.Currency,
		Vendor: result.Vendor, Livemode: livemode, CreatedAt: result.CreatedAt, UpdatedAt: result.UpdatedAt,
	}
}

// CreatePayinHandler implements POST /api/v1/b2b/payins (b2bCreatePayinV1,
// docs/reference/c1-b2b-design.md §3.4). Must be mounted behind
// auth.RequireMerchantAuth, auth.RequireScope(opCreatePayin), and
// quota.RequireQuota — this handler assumes a Principal is already in
// context and quota has already been enforced by the time it runs.
func CreatePayinHandler(payinClient *client.PayinClient, idemSvc *idempotency.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			response.Unauthorized(w, "authentication required")
			return
		}

		var req createPayinRequest
		body, ok := readJSONBody(w, r, &req)
		if !ok {
			return
		}
		amount, ok := validateAmount(req.Amount)
		if !ok {
			writeValidationError(w, "amount must be a positive integer decimal string")
			return
		}
		if !validateCurrency(req.Currency) {
			writeValidationError(w, "currency must be a 3-letter uppercase ISO 4217 code")
			return
		}

		decision, proceed := beginIdempotentWrite(w, r, idemSvc, principal.TenantID, opCreatePayin, body)
		if !proceed {
			return
		}

		result, err := payinClient.CreateTopupIntent(r.Context(), principal.TenantID, principal.Environment, req.Currency, amount.String(), decision.DownstreamKey)
		if err != nil {
			_, code := ownerErrorStatus(err)
			failIdempotentWrite(w, r, idemSvc, principal.TenantID, decision.RecordID, code, func(w http.ResponseWriter) { writeOwnerError(w, err) })
			return
		}

		completeIdempotentWrite(w, r, idemSvc, principal.TenantID, decision.RecordID, http.StatusCreated,
			payinResponseFromResult(result, principal.Environment == "live"), result.ID.String())
	}
}

// GetPayinHandler implements GET /api/v1/b2b/payins/{payin_id}
// (b2bGetPayinV1). Must be mounted behind auth.RequireMerchantAuth,
// auth.RequireScope(opGetPayin), and quota.RequireQuota. Tenant scoping
// happens inside payinClient.GetTopupIntent (via PayinService's own
// GetMerchantTopupIntent, §7.3) — this handler never re-derives it.
func GetPayinHandler(payinClient *client.PayinClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			response.Unauthorized(w, "authentication required")
			return
		}
		id, err := uuid.Parse(r.PathValue("payin_id"))
		if err != nil {
			writeValidationError(w, "payin_id must be a valid UUID")
			return
		}
		result, err := payinClient.GetTopupIntent(r.Context(), principal.TenantID, id)
		if err != nil {
			writeOwnerError(w, err)
			return
		}
		response.OK(w, payinResponseFromResult(result, principal.Environment == "live"))
	}
}
