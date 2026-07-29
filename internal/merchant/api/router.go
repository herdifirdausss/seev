package api

import (
	"net/http"

	"github.com/herdifirdausss/seev/internal/merchant/auth"
	"github.com/herdifirdausss/seev/internal/merchant/client"
	"github.com/herdifirdausss/seev/internal/merchant/idempotency"
	"github.com/herdifirdausss/seev/internal/merchant/quota"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/pkg/httpcontract"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

// Deps is everything NewRouter needs to mount the B2B payin/payout
// surface — deliberately narrower than the full internal/merchant.Module
// (repository interfaces plus already-constructed service objects), so
// this package's own dependency list stays exactly what its handlers use.
type Deps struct {
	APIKeys       repository.APIKeyRepository
	Tenants       repository.TenantRepository
	APIKeyPepper  string
	QuotaEnforcer *quota.Enforcer
	Idempotency   *idempotency.Service
	Payin         *client.PayinClient
	Payout        *client.PayoutClient
	// Ledger is Plan 57 T10 follow-up's addition — backs the merchant
	// profile/accounts/transactions/transfers surface (§6.4) that T5/T6
	// built the owner-service RPCs for but never wired to HTTP.
	Ledger *client.LedgerClient
}

// NewRouter builds the B2B payin/payout HTTP surface
// (docs/roadmap/active/57-c1-merchant-b2b-api.md §6.4). Registered
// patterns are relative ("/payins", not "/api/v1/b2b/payins") — the caller
// mounts this behind its own "/api/v1/b2b" StripPrefix, the same
// convention internal/handler.NewRouter already uses for its own sub-mux
// nesting (apiMux under apiRoot).
//
// Every route shares the identical T3/T4 middleware ORDER (§7.2: "scope
// evaluation occurs after key and tenant validation"): auth ->
// scope -> quota. Idempotency is NOT a middleware — it is a service each
// create handler calls directly (internal/merchant/idempotency.Service has
// no HTTP wrapper, only Begin/Complete/Fail), since a read has nothing to
// claim.
func NewRouter(deps Deps) http.Handler {
	requireAuth := auth.RequireMerchantAuth(deps.APIKeys, deps.Tenants, deps.APIKeyPepper)

	mux := httpcontract.New(httpcontract.Options{Owner: "gateway", Audience: "merchant", Contract: "b2b-v1"})

	route := func(pattern, operationID, quotaClass string, isWrite bool, handler http.Handler) {
		chain := middleware.Chain(
			requireAuth,
			auth.RequireScope(operationID),
			quota.RequireQuota(deps.QuotaEnforcer, quotaClass, isWrite),
		)
		mux.HandleContract(pattern, chain(handler), httpcontract.Registration{OperationID: operationID})
	}

	// Quota classes per §9.1: "payin"/"payout" for their own financial
	// writes, "read" for every GET — none of these are the generic
	// "write" class, since the plan reserves per-resource classes for
	// exactly this reason (a payin surge must not exhaust a merchant's
	// payout quota budget, or vice versa).
	route("POST /payins", opCreatePayin, "payin", true, CreatePayinHandler(deps.Payin, deps.Idempotency))
	route("GET /payins/{payin_id}", opGetPayin, "read", false, GetPayinHandler(deps.Payin))
	route("POST /payouts", opCreatePayout, "payout", true, CreatePayoutHandler(deps.Payout, deps.Idempotency))
	route("GET /payouts/{payout_id}", opGetPayout, "read", false, GetPayoutHandler(deps.Payout))

	// Merchant profile, accounts, transactions, and transfers (Plan 57 T10
	// follow-up) — "transfers" is its own quota class, same reasoning as
	// payin/payout above; every other route here is a read.
	route("GET /merchant", opGetMerchant, "read", false, GetMerchantHandler(deps.Tenants))
	route("GET /accounts", opListAccounts, "read", false, ListAccountsHandler(deps.Ledger))
	route("GET /accounts/{account_id}", opGetAccount, "read", false, GetAccountHandler(deps.Ledger))
	route("GET /accounts/{account_id}/balance", opGetAccountBalance, "read", false, GetAccountBalanceHandler(deps.Ledger))
	route("GET /transactions", opListTransactions, "read", false, ListTransactionsHandler(deps.Ledger))
	route("GET /transactions/{transaction_id}", opGetTransaction, "read", false, GetTransactionHandler(deps.Ledger))
	route("POST /transfers", opCreateTransfer, "transfers", true, CreateTransferHandler(deps.Ledger, deps.Idempotency))
	route("GET /transfers/{transaction_id}", opGetTransfer, "read", false, GetTransactionHandler(deps.Ledger))

	return mux
}
