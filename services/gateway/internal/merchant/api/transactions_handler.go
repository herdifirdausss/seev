package api

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/auth"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/client"
)

const (
	opListTransactions = "b2bListTransactionsV1"
	opGetTransaction   = "b2bGetTransactionV1"
)

// defaultListLimit/maxListLimit mirror contracts/http/components/common.yaml's
// shared cursor pagination parameters (default: 25, maximum: 100) — every
// B2B list endpoint uses the identical bounds.
const (
	defaultListLimit = 25
	maxListLimit     = 100
)

type transactionResponse struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Status               string `json:"status"`
	Amount               string `json:"amount"`
	Currency             string `json:"currency"`
	SourceAccountID      string `json:"source_account_id"`
	DestinationAccountID string `json:"destination_account_id"`
	CreatedAt            string `json:"created_at"`
}

type listTransactionsResponse struct {
	Data       []transactionResponse `json:"data"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

func transactionResponseFromResult(tx client.TransactionResult) transactionResponse {
	return transactionResponse{
		ID: tx.ID.String(), Type: tx.Type, Status: tx.Status, Amount: tx.Amount.String(), Currency: tx.Currency,
		SourceAccountID: tx.SourceAccountID.String(), DestinationAccountID: tx.DestinationAccountID.String(),
		CreatedAt: tx.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z"),
	}
}

// parseListLimit reads and bounds the shared "limit" query parameter — an
// out-of-range or unparseable value falls back to defaultListLimit rather
// than erroring, matching this codebase's other list endpoints' own
// permissive-default convention.
func parseListLimit(r *http.Request) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultListLimit
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maxListLimit {
		return defaultListLimit
	}
	return limit
}

// ListTransactionsHandler implements GET /api/v1/b2b/transactions
// (b2bListTransactionsV1) — every transaction touching the tenant's own
// account, newest first, opaque-cursor paginated.
func ListTransactionsHandler(ledgerClient *client.LedgerClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			response.Unauthorized(w, "authentication required")
			return
		}
		beforeCreatedAt, beforeID, err := decodeCursor(r.URL.Query().Get("cursor"))
		if err != nil {
			writeValidationError(w, "invalid cursor")
			return
		}
		limit := parseListLimit(r)
		txs, err := ledgerClient.ListTransactions(r.Context(), principal.TenantID, beforeCreatedAt, beforeID, limit)
		if err != nil {
			writeOwnerError(w, err)
			return
		}
		out := listTransactionsResponse{Data: make([]transactionResponse, 0, len(txs))}
		for _, tx := range txs {
			out.Data = append(out.Data, transactionResponseFromResult(tx))
		}
		if len(txs) == limit {
			last := txs[len(txs)-1]
			out.NextCursor = encodeCursor(last.CreatedAt, last.ID)
		}
		response.OK(w, out)
	}
}

// GetTransactionHandler implements GET /api/v1/b2b/transactions/{transaction_id}
// (b2bGetTransactionV1) — also backs GetTransferHandler's read
// (b2bGetTransferV1): a merchant transaction has no separate "transfer"
// resource, only a Type value on the same underlying resource (§6.4).
// Tenant scoping happens inside LedgerService's own GetMerchantTransaction
// RPC (§7.3) — this handler never re-derives it.
func GetTransactionHandler(ledgerClient *client.LedgerClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			response.Unauthorized(w, "authentication required")
			return
		}
		id, err := uuid.Parse(r.PathValue("transaction_id"))
		if err != nil {
			writeValidationError(w, "transaction_id must be a valid UUID")
			return
		}
		tx, err := ledgerClient.GetTransaction(r.Context(), principal.TenantID, id)
		if err != nil {
			writeOwnerError(w, err)
			return
		}
		response.OK(w, transactionResponseFromResult(tx))
	}
}
