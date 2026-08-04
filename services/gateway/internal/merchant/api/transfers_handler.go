package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/auth"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/client"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/idempotency"
)

const (
	opCreateTransfer = "b2bCreateTransferV1"
	opGetTransfer    = "b2bGetTransferV1"
)

type createTransferRequest struct {
	DestinationAccountID string         `json:"destination_account_id"`
	Amount               string         `json:"amount"`
	Currency             string         `json:"currency"`
	ReferenceID          string         `json:"reference_id,omitempty"`
	Metadata             map[string]any `json:"metadata,omitempty"`
}

// CreateTransferHandler implements POST /api/v1/b2b/transfers
// (b2bCreateTransferV1, contracts/http/b2b-v1.yaml's CreateTransferRequest).
// The source account is ALWAYS the authenticated tenant's own resolved
// cash account (services/ledger/internal/processors/merchant_transfer.go's own
// "source is structurally never caller-supplied" guarantee) — this
// handler has no source-account-id field to even accept.
func CreateTransferHandler(ledgerClient *client.LedgerClient, idemSvc *idempotency.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			response.Unauthorized(w, "authentication required")
			return
		}

		var req createTransferRequest
		body, ok := readJSONBody(w, r, &req)
		if !ok {
			return
		}
		destinationAccountID, err := uuid.Parse(req.DestinationAccountID)
		if err != nil {
			writeValidationError(w, "destination_account_id must be a valid UUID")
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

		decision, proceed := beginIdempotentWrite(w, r, idemSvc, principal.TenantID, opCreateTransfer, body)
		if !proceed {
			return
		}

		tx, err := ledgerClient.CreateTransfer(r.Context(), principal.TenantID, destinationAccountID, amount, req.Currency, decision.DownstreamKey)
		if err != nil {
			_, code := ownerErrorStatus(err)
			failIdempotentWrite(w, r, idemSvc, principal.TenantID, decision.RecordID, code, func(w http.ResponseWriter) { writeOwnerError(w, err) })
			return
		}

		completeIdempotentWrite(w, r, idemSvc, principal.TenantID, decision.RecordID, http.StatusCreated,
			transactionResponseFromResult(tx), tx.ID.String())
	}
}
