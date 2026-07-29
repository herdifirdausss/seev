package api

import (
	"encoding/json"
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
	opCreatePayout = "b2bCreatePayoutV1"
	opGetPayout    = "b2bGetPayoutV1"
)

type createPayoutRequest struct {
	Amount      string          `json:"amount"`
	Currency    string          `json:"currency"`
	Destination json.RawMessage `json:"destination"`
	ReferenceID string          `json:"reference_id,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
}

type payoutResponse struct {
	ID           string    `json:"id"`
	Status       string    `json:"status"`
	Amount       string    `json:"amount"`
	Currency     string    `json:"currency"`
	Vendor       string    `json:"vendor"`
	ErrorMessage string    `json:"error_message,omitempty"`
	Livemode     bool      `json:"livemode"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func payoutResponseFromResult(result client.PayoutResult, livemode bool) payoutResponse {
	return payoutResponse{
		ID: result.ID.String(), Status: mapPayoutStatus(result.Status), Amount: result.Amount, Currency: result.Currency,
		Vendor: result.Vendor, ErrorMessage: result.ErrorMessage, Livemode: livemode,
		CreatedAt: result.CreatedAt, UpdatedAt: result.UpdatedAt,
	}
}

// CreatePayoutHandler implements POST /api/v1/b2b/payouts
// (b2bCreatePayoutV1, docs/reference/c1-b2b-design.md §3.5). Must be
// mounted behind auth.RequireMerchantAuth, auth.RequireScope(opCreatePayout),
// and quota.RequireQuota — this handler assumes a Principal is already in
// context and quota has already been enforced by the time it runs.
func CreatePayoutHandler(payoutClient *client.PayoutClient, idemSvc *idempotency.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			response.Unauthorized(w, "authentication required")
			return
		}

		var req createPayoutRequest
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
		if len(req.Destination) == 0 {
			writeValidationError(w, "destination is required")
			return
		}

		decision, proceed := beginIdempotentWrite(w, r, idemSvc, principal.TenantID, opCreatePayout, body)
		if !proceed {
			return
		}

		// createdBy is an audit/trace string only (payout_requests.created_by)
		// — never an identity Gateway derives owner-service behavior from.
		// The API key's own id is the most specific actor identity available
		// at this edge.
		createdBy := "b2b_api_key:" + principal.KeyID.String()
		result, err := payoutClient.CreatePayout(r.Context(), principal.TenantID, principal.Environment, req.Currency, amount.String(), req.Destination, createdBy, decision.DownstreamKey)
		if err != nil {
			_, code := ownerErrorStatus(err)
			failIdempotentWrite(w, r, idemSvc, principal.TenantID, decision.RecordID, code, func(w http.ResponseWriter) { writeOwnerError(w, err) })
			return
		}

		completeIdempotentWrite(w, r, idemSvc, principal.TenantID, decision.RecordID, http.StatusCreated,
			payoutResponseFromResult(result, principal.Environment == "live"), result.ID.String())
	}
}

// GetPayoutHandler implements GET /api/v1/b2b/payouts/{payout_id}
// (b2bGetPayoutV1). Must be mounted behind auth.RequireMerchantAuth,
// auth.RequireScope(opGetPayout), and quota.RequireQuota. Tenant scoping
// happens inside payoutClient.GetPayout (via PayoutService's own
// GetMerchant, §7.3) — this handler never re-derives it.
func GetPayoutHandler(payoutClient *client.PayoutClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			response.Unauthorized(w, "authentication required")
			return
		}
		id, err := uuid.Parse(r.PathValue("payout_id"))
		if err != nil {
			writeValidationError(w, "payout_id must be a valid UUID")
			return
		}
		result, err := payoutClient.GetPayout(r.Context(), principal.TenantID, id)
		if err != nil {
			writeOwnerError(w, err)
			return
		}
		response.OK(w, payoutResponseFromResult(result, principal.Environment == "live"))
	}
}
