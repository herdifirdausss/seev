package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/auth"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/client"
)

const (
	opListAccounts      = "b2bListAccountsV1"
	opGetAccount        = "b2bGetAccountV1"
	opGetAccountBalance = "b2bGetAccountBalanceV1"
)

type accountResponse struct {
	ID       string `json:"id"`
	Currency string `json:"currency"`
	Balance  string `json:"balance"`
	Status   string `json:"status"`
}

type accountBalanceResponse struct {
	AccountID string `json:"account_id"`
	Currency  string `json:"currency"`
	Balance   string `json:"balance"`
}

func accountResponseFromResult(acc client.AccountResult) accountResponse {
	return accountResponse{ID: acc.AccountID.String(), Currency: acc.Currency, Balance: acc.Balance.String(), Status: acc.Status}
}

// ListAccountsHandler implements GET /api/v1/b2b/accounts (b2bListAccountsV1).
// A merchant tenant has exactly one ledger account today (its cash
// account, provisioned at tenant creation) — this always returns a
// single-element list, never paginated; cursor/limit query params are
// accepted (contract-required) but have no effect yet.
func ListAccountsHandler(ledgerClient *client.LedgerClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			response.Unauthorized(w, "authentication required")
			return
		}
		acc, err := ledgerClient.GetAccount(r.Context(), principal.TenantID)
		if err != nil {
			writeOwnerError(w, err)
			return
		}
		response.OK(w, []accountResponse{accountResponseFromResult(acc)})
	}
}

// GetAccountHandler implements GET /api/v1/b2b/accounts/{account_id}
// (b2bGetAccountV1). account_id must match the tenant's OWN resolved cash
// account — any other id, including a real account belonging to a
// different tenant, returns 404 rather than leaking existence (§6.7).
func GetAccountHandler(ledgerClient *client.LedgerClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			response.Unauthorized(w, "authentication required")
			return
		}
		requestedID, err := uuid.Parse(r.PathValue("account_id"))
		if err != nil {
			writeValidationError(w, "account_id must be a valid UUID")
			return
		}
		acc, err := ledgerClient.GetAccount(r.Context(), principal.TenantID)
		if err != nil {
			writeOwnerError(w, err)
			return
		}
		if acc.AccountID != requestedID {
			response.ErrorStatus(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "account not found")
			return
		}
		response.OK(w, accountResponseFromResult(acc))
	}
}

// GetAccountBalanceHandler implements GET
// /api/v1/b2b/accounts/{account_id}/balance (b2bGetAccountBalanceV1) —
// same tenant-scoping rule as GetAccountHandler, narrowed to the balance
// fields only.
func GetAccountBalanceHandler(ledgerClient *client.LedgerClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			response.Unauthorized(w, "authentication required")
			return
		}
		requestedID, err := uuid.Parse(r.PathValue("account_id"))
		if err != nil {
			writeValidationError(w, "account_id must be a valid UUID")
			return
		}
		acc, err := ledgerClient.GetAccount(r.Context(), principal.TenantID)
		if err != nil {
			writeOwnerError(w, err)
			return
		}
		if acc.AccountID != requestedID {
			response.ErrorStatus(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "account not found")
			return
		}
		response.OK(w, accountBalanceResponse{AccountID: acc.AccountID.String(), Currency: acc.Currency, Balance: acc.Balance.String()})
	}
}
