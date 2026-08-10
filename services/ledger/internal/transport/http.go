package transport

import (
	"log/slog"
	"net/http"
	"time"

	fraudcheck "github.com/herdifirdausss/seev/contracts/clients/fraud"
	"github.com/herdifirdausss/seev/internal/platform/transport/httpcontract"
	"github.com/herdifirdausss/seev/services/ledger/internal/feepolicy"
	commandservice "github.com/herdifirdausss/seev/services/ledger/internal/ledger/command"
)

const (
	defaultEntriesLimit = 50
	maxEntriesLimit     = 200
	// maxStatementDays caps a statement request's [from, to] span
	// (docs/roadmap/archive/15 Task T2, decision K7) — checked here (cheap, no DB
	// touch) before calling the service, which separately enforces the
	// row-count cap (needs actual query results).
	maxStatementDays = 92
)

// adminOnlyTypes are transaction types that must never be triggered without
// an admin JWT — compliance/correction actions (docs/roadmap/archive/05 Task 1b.4).
// Enforced on BOTH routers as defense in depth, even though the internal
// router is already network-isolated (docs/roadmap/archive/10 Task T1).
//
// adjustment_credit/adjustment_debit/reversal/chargeback/freeze_release/freeze_confiscate
// are deliberately ABSENT here — as of docs/roadmap/archive/16 Task T1 (and, for the
// latter four, later security audit findings), none of them are merely
// admin-gated: they are blocked from direct POST entirely (see
// governance.IsWorkflowOnlyPostType) and only reachable via the maker-checker
// /admin/adjustments flow. A single admin JWT must not let one identity both
// decide and execute an unrestricted reversal, remove a protective freeze,
// or permanently seize funds; these are no less consequential than a manual
// balance adjustment, which already required a second identity.
var adminOnlyTypes = map[string]bool{
	"freeze_initiate": true,
}

// publicUserTypes are the ONLY transaction types reachable from the
// public-facing router (NewRouter). Everything else — money movement to/from
// system accounts (money_in, refund, withdraw settlement, escrow release,
// fee_collect) plus the adminOnlyTypes above — is only reachable via the
// internal router (NewInternalRouter, docs/roadmap/archive/10 Task T1). This closes the
// hole where any authenticated user could credit their own cash from a
// settlement account with no real deposit behind it.
var publicUserTypes = map[string]bool{
	"transfer_p2p":      true,
	"transfer_pocket":   true,
	"withdraw_initiate": true,
	"escrow_hold":       true,
}

// NewRouter builds the public-facing HTTP handler for the ledger module. The
// caller (the composition root, services/gateway/internal/transport/http/router.go) is responsible
// for mounting it under a path prefix and wrapping it with auth/rate-limit
// middleware — this router assumes every request already carries a valid
// JWT. Only publicUserTypes are postable here; every other transaction type
// is rejected with 403. See NewInternalRouter for the unrestricted variant.
func NewRouter(svc Service) http.Handler {
	return NewRouterWithPolicy(svc, nil)
}

// NewRouterWithPolicy is NewRouter plus a policy engine (docs/roadmap/archive/17 Task
// T1) — evaluated before every publicUserTypes posting. Pass nil for
// policy to get NewRouter's behavior exactly (no limit checks at all).
func NewRouterWithPolicy(svc Service, policy PolicyChecker) http.Handler {
	return NewRouterWithOptions(svc, policy, nil)
}

// NewRouterWithOptions is NewRouterWithPolicy plus a database fee policy.
// A nil feePolicy keeps fees disabled, which is useful for isolated tests.
func NewRouterWithOptions(svc Service, policy PolicyChecker, feePolicy *feepolicy.Policy) http.Handler {
	return NewRouterWithFraud(svc, policy, feePolicy, nil, nil, 0)
}

// NewRouterWithFraud is NewRouterWithOptions plus a fraud screening client
// (docs/roadmap/archive/37): every public-router posting is screened BEFORE svc.Post
// is called — before any DB transaction opens, unlike the old in-transaction
// PrePostHook seam this replaces. A nil fraudClient disables screening
// entirely (same "absent = no-op" convention as policy/feePolicy). A nil
// logger falls back to slog.Default(). feeQuoteTTL (docs/roadmap/archive/38 Task T3)
// <=0 falls back to feepolicy.DefaultQuoteTTL.
func NewRouterWithFraud(svc Service, policy PolicyChecker, feePolicy *feepolicy.Policy, fraudClient *fraudcheck.Client, logger *slog.Logger, feeQuoteTTL time.Duration) http.Handler {
	return NewRouterWithFraudAndExecutor(svc, policy, feePolicy, fraudClient, logger, feeQuoteTTL, nil)
}

// NewRouterWithFraudAndExecutor is the production constructor. Policy checks
// are executed by the shared command boundary after this handler has decoded
// and normalized the request; this transport does not own authorization.
func NewRouterWithFraudAndExecutor(svc Service, policy PolicyChecker, feePolicy *feepolicy.Policy, fraudClient *fraudcheck.Client, logger *slog.Logger, feeQuoteTTL time.Duration, executor *commandservice.Executor) http.Handler {
	return NewRouterWithFraudAndExecutorAndFeeApproval(svc, policy, feePolicy, fraudClient, logger, feeQuoteTTL, executor, nil)
}

func NewRouterWithFraudAndExecutorAndFeeApproval(svc Service, policy PolicyChecker, feePolicy *feepolicy.Policy, fraudClient *fraudcheck.Client, logger *slog.Logger, feeQuoteTTL time.Duration, executor *commandservice.Executor, feeApproval *feepolicy.Approval) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	h := &handler{svc: svc, allowedTypes: publicUserTypes, feePolicy: feePolicy, policy: policy, executor: executor, feeApproval: feeApproval, fraudClient: fraudClient, logger: logger, feeQuoteTTL: feeQuoteTTL}
	return h.mux()
}

// NewInternalRouter builds the HTTP handler meant for the internal-only
// listener (INTERNAL_APP_PORT, bound to 127.0.0.1 by default — see
// services/gateway/internal/transport/http/router.go NewInternalRouter and services/gateway/cmd/gateway/main.go). It
// accepts every registered transaction type — including money_in, refund,
// withdraw settlement, escrow release, fee_collect — because those are
// legitimately triggered by trusted internal callers (payment-gateway
// webhook handlers, ops tooling), never by an end user directly. Compliance
// actions (adminOnlyTypes) remain admin-gated even here.
func NewInternalRouter(svc Service) http.Handler {
	return NewInternalRouterWithFeePolicy(svc, nil)
}

func NewInternalRouterWithFeePolicy(svc Service, feePolicy *feepolicy.Policy) http.Handler {
	return NewInternalRouterWithExecutorAndFeePolicy(svc, feePolicy, nil)
}

// NewInternalRouterWithExecutorAndFeePolicy keeps trusted internal routes on
// the same command boundary. The executor identifies this source as an
// internal service, so end-user velocity limits are not applied twice.
func NewInternalRouterWithExecutorAndFeePolicy(svc Service, feePolicy *feepolicy.Policy, executor *commandservice.Executor) http.Handler {
	return NewInternalRouterWithExecutorAndFeeApproval(svc, feePolicy, executor, nil)
}

func NewInternalRouterWithExecutorAndFeeApproval(svc Service, feePolicy *feepolicy.Policy, executor *commandservice.Executor, feeApproval *feepolicy.Approval) http.Handler {
	h := &handler{svc: svc, allowedTypes: nil, feePolicy: feePolicy, executor: executor, feeApproval: feeApproval}
	return h.mux()
}

func (h *handler) mux() http.Handler {
	audience, contract := "internal", "internal-v1"
	if h.allowedTypes != nil {
		audience, contract = "public", "public-v1"
	}
	mux := httpcontract.New(httpcontract.Options{Owner: "ledger", Audience: audience, Contract: contract})

	mux.HandleFunc("POST /transactions", h.postTransaction)
	mux.HandleFunc("GET /transactions/{id}", h.getTransaction)
	mux.HandleFunc("GET /accounts", h.listAccounts)
	mux.HandleFunc("GET /accounts/{id}/balance", h.getBalance)
	mux.HandleFunc("GET /accounts/{id}/entries", h.listEntries)
	mux.HandleFunc("GET /accounts/{id}/statement", h.getStatement)
	mux.HandleFunc("POST /accounts/pockets", h.createPocket)

	// C4 multi-currency wallet and FX surface. The existing gateway already
	// forwards /api/v1/ledger/* to this router, so these routes are available
	// without introducing a second FX service or a second balance owner.
	mux.HandleFunc("GET /currencies", h.listCurrencies)
	mux.HandleFunc("GET /balances", h.listCurrencyBalances)
	mux.HandleFunc("GET /balances/{currency}", h.getCurrencyBalance)
	mux.HandleFunc("POST /currencies/{currency}/enable", h.enableCurrency)
	mux.HandleFunc("GET /fx/pairs", h.listFXPairs)
	mux.HandleFunc("POST /fx/quotes", h.createFXQuote)
	mux.HandleFunc("GET /fx/quotes/{id}", h.getFXQuote)
	mux.HandleFunc("POST /fx/conversions", h.createFXConversion)
	mux.HandleFunc("GET /fx/conversions/{id}", h.getFXConversion)

	// Fee quotes (docs/roadmap/archive/38 Task T3) — PUBLIC router only: this is the
	// "what will I pay" preview an end user requests before committing to a
	// transaction. Reachable automatically as
	// /api/v1/ledger/fees/quote via the existing gateway proxy, no gateway
	// change needed.
	if h.allowedTypes != nil {
		mux.HandleFunc("POST /fees/quote", h.createQuote)
	}

	// Scheduled transactions (docs/roadmap/archive/19 Task T1) — on BOTH routers, a
	// user manages only their own schedules (ownership enforced in
	// services/ledger/internal/ledger/schedule), same as listAccounts/getBalance above.
	mux.HandleFunc("POST /schedules", h.createSchedule)
	mux.HandleFunc("GET /schedules", h.listSchedules)
	mux.HandleFunc("POST /schedules/{id}/pause", h.pauseSchedule)
	mux.HandleFunc("POST /schedules/{id}/resume", h.resumeSchedule)
	mux.HandleFunc("POST /schedules/{id}/cancel", h.cancelSchedule)
	mux.HandleFunc("POST /schedules/{id}/confirm-fee-cap", h.confirmScheduledFeeCap)
	mux.HandleFunc("GET /schedules/{id}/occurrences", h.listScheduledOccurrences)
	mux.HandleFunc("GET /schedules/{id}/occurrences/{occurrence_id}", h.getScheduledOccurrence)
	// C5 names the same durable history as scheduled-transactions/executions;
	// keep both spellings additive while existing clients migrate.
	mux.HandleFunc("GET /scheduled-transactions/{id}/executions", h.listScheduledOccurrences)
	mux.HandleFunc("GET /scheduled-transactions/{id}/executions/{execution_id}", h.getScheduledOccurrence)
	mux.HandleFunc("POST /scheduled-transactions/{id}/confirm-fee-cap", h.confirmScheduledFeeCap)

	// C5 user read surfaces. Writes remain internal/admin-controlled; these
	// reads are additive and use optional structural interfaces so existing
	// transport mocks do not need a generated contract change.
	mux.HandleFunc("GET /savings/products", h.listSavingsProducts)
	mux.HandleFunc("GET /savings/enrollments", h.listSavingsEnrollments)
	mux.HandleFunc("GET /savings/enrollments/{id}", h.getSavingsEnrollment)
	mux.HandleFunc("GET /savings/enrollments/{id}/accruals", h.listInterestAccruals)
	mux.HandleFunc("GET /savings/enrollments/{id}/periods", h.listInterestPeriods)

	// Admin outbox dead-letter replay (docs/roadmap/archive/12 Task T3) — internal
	// router ONLY (allowedTypes == nil is this package's existing signal
	// for "this is the internal router", set by NewInternalRouter). Also
	// admin-gated inside the handlers themselves — defense in depth, since
	// network isolation of the internal listener is the primary control,
	// not the only one, for an operation this sensitive.
	if h.allowedTypes == nil {
		// C6 data-migration control plane — typed Ledger-owned admin API only;
		// the Admin BFF proxies these routes and never queries migration tables.
		mux.HandleFunc("GET /admin/migrations", h.listMigrations)
		mux.HandleFunc("GET /admin/migrations/{id}", h.getMigration)
		mux.HandleFunc("POST /admin/migrations/{id}/transition", h.transitionMigration)
		mux.HandleFunc("POST /admin/migrations/{id}/read-percentage", h.setMigrationReadPercentage)
		mux.HandleFunc("POST /admin/migrations/{id}/dual-write", h.setMigrationDualWrite)
		mux.HandleFunc("POST /admin/migrations/{id}/pause", h.pauseMigration)
		mux.HandleFunc("POST /admin/migrations/{id}/resume", h.resumeMigration)
		mux.HandleFunc("GET /admin/migrations/{id}/mismatches", h.listMigrationMismatches)
		mux.HandleFunc("POST /admin/migrations/{id}/reconcile", h.runMigrationReconciliation)
		mux.HandleFunc("POST /admin/migrations/{id}/mismatches/{mismatch_id}/repair", h.requestMigrationRepair)
		mux.HandleFunc("POST /admin/migrations/{id}/repairs/{repair_id}/approve", h.approveMigrationRepair)

		mux.HandleFunc("POST /admin/outbox/dead/{id}/replay", h.replayDeadEvent)
		mux.HandleFunc("POST /admin/outbox/dead/replay-all", h.replayAllDeadEvents)
		mux.HandleFunc("GET /admin/outbox/dead", h.listDeadEvents)

		// Maker-checker adjustment governance (docs/roadmap/archive/16 Task T1) —
		// internal router only, admin-gated inside each handler. This is
		// the ONLY reachable path to adjustment_credit/adjustment_debit —
		// see the governance.IsWorkflowOnlyPostType check in postTransaction.
		mux.HandleFunc("POST /admin/adjustments", h.createAdjustment)
		mux.HandleFunc("POST /admin/adjustments/{id}/approve", h.approveAdjustment)
		mux.HandleFunc("POST /admin/adjustments/{id}/reject", h.rejectAdjustment)
		mux.HandleFunc("GET /admin/adjustments", h.listAdjustments)
		mux.HandleFunc("GET /admin/adjustments/{id}", h.getAdjustment)

		// External reconciliation (docs/roadmap/archive/16 Task T2) — internal router
		// only, admin-gated. Resolve creates a pending adjustment via the
		// same maker-checker path above; it never moves money by itself.
		mux.HandleFunc("POST /admin/recon/batches", h.createReconBatch)
		mux.HandleFunc("GET /admin/recon/batches", h.listReconBatches)
		mux.HandleFunc("GET /admin/recon/batches/{id}", h.getReconBatch)
		mux.HandleFunc("POST /admin/recon/items/{id}/resolve", h.resolveReconItem)

		// Schedule runner ops/testing trigger (docs/roadmap/archive/19 Task T1 step 5)
		// — internal router only, admin-gated.
		mux.HandleFunc("POST /admin/schedules/run", h.runSchedulesNow)
		mux.HandleFunc("GET /admin/schedules/{id}/occurrences", h.listAdminScheduledOccurrences)
		mux.HandleFunc("POST /admin/schedules/occurrences/{occurrence_id}/retry", h.retryScheduledOccurrence)
		mux.HandleFunc("GET /admin/scheduled-transactions/{id}/executions", h.listAdminScheduledOccurrences)
		mux.HandleFunc("POST /admin/scheduled-transactions/{id}/executions/{occurrence_id}/retry", h.retryScheduledOccurrence)

		// Batch disbursement (docs/roadmap/archive/19 Task T2) — internal router only,
		// admin-gated. approve/reject (maker-checker gate, business-completeness
		// audit finding) require checker privileges specifically; create
		// requires maker privileges; run/report stay generic admin.
		mux.HandleFunc("POST /admin/disbursements", h.createDisbursementBatch)
		mux.HandleFunc("POST /admin/disbursements/{id}/approve", h.approveDisbursementBatch)
		mux.HandleFunc("POST /admin/disbursements/{id}/reject", h.rejectDisbursementBatch)
		mux.HandleFunc("POST /admin/disbursements/{id}/run", h.runDisbursement)
		mux.HandleFunc("GET /admin/disbursements/{id}", h.getDisbursementReport)

		// Interest accrual (docs/roadmap/archive/19 Task T3) — internal router only,
		// admin-gated.
		mux.HandleFunc("PUT /admin/savings/{account_id}", h.setSavingsConfig)
		mux.HandleFunc("GET /admin/savings", h.listSavingsConfigs)
		mux.HandleFunc("POST /admin/savings/products", h.createSavingsProduct)
		mux.HandleFunc("GET /admin/savings/products", h.listSavingsProducts)
		mux.HandleFunc("POST /admin/savings/products/{id}/status", h.updateSavingsProductStatus)
		mux.HandleFunc("POST /admin/savings/products/{id}/rates", h.createSavingsRate)
		mux.HandleFunc("POST /admin/savings/rates/{id}/submit", h.submitSavingsRate)
		mux.HandleFunc("POST /admin/savings/rates/{id}/approve", h.approveSavingsRate)
		mux.HandleFunc("POST /admin/savings/rates/{id}/reject", h.rejectSavingsRate)
		mux.HandleFunc("POST /admin/savings/enrollments", h.createSavingsEnrollment)
		mux.HandleFunc("POST /admin/savings/enrollments/{id}/pause", h.pauseSavingsEnrollment)
		mux.HandleFunc("POST /admin/savings/enrollments/{id}/resume", h.resumeSavingsEnrollment)
		mux.HandleFunc("POST /admin/savings/enrollments/{id}/end", h.endSavingsEnrollment)
		mux.HandleFunc("GET /admin/savings/periods/{id}/preview", h.previewInterestPeriod)
		mux.HandleFunc("POST /admin/savings/periods/{id}/close", h.closeInterestPeriod)
		mux.HandleFunc("POST /admin/savings/interest/run", h.runInterestDaily)
		mux.HandleFunc("POST /admin/savings/period-items/{id}/retry", h.retryInterestPeriodItem)
		mux.HandleFunc("POST /admin/savings/adjustments", h.createInterestAdjustment)
		mux.HandleFunc("POST /admin/savings/adjustments/{id}/approve", h.approveInterestAdjustment)

		mux.HandleFunc("GET /admin/reports/{kind}", h.getReport)

		// Database-driven fee management (docs/roadmap/archive/33 Task T3). Disable is
		// represented by enabled=false so pricing history remains auditable.
		mux.HandleFunc("GET /admin/ledger/fee-rules", h.listFeeRules)
		mux.HandleFunc("GET /admin/ledger/fee-rules/{id}/versions", h.listFeeRuleVersions)
		mux.HandleFunc("POST /admin/ledger/fee-rules", h.createFeeRule)
		mux.HandleFunc("PUT /admin/ledger/fee-rules/{id}", h.updateFeeRule)
		mux.HandleFunc("POST /admin/ledger/fee-rules/{id}/submit", h.submitFeeRule)
		mux.HandleFunc("POST /admin/ledger/fee-rules/{id}/approve", h.approveFeeRule)
		mux.HandleFunc("POST /admin/ledger/fee-rules/{id}/reject", h.rejectFeeRule)

		// Chargeback case management is Ledger-owned. These routes never post
		// money directly; any chargeback transaction remains a separate,
		// audited command.
		mux.HandleFunc("POST /admin/disputes", h.openDispute)
		mux.HandleFunc("GET /admin/disputes", h.listDisputes)
		mux.HandleFunc("GET /admin/disputes/{id}", h.getDispute)
		mux.HandleFunc("GET /admin/disputes/{id}/status-changes", h.listDisputeStatusChanges)
		mux.HandleFunc("POST /admin/disputes/{id}/evidence", h.submitDisputeEvidence)
		mux.HandleFunc("POST /admin/disputes/{id}/resolve", h.resolveDispute)

		// FX operator controls stay on Ledger's internal listener. Admin BFF
		// proxies these routes; it never opens a Ledger database connection.
		mux.HandleFunc("GET /admin/fx/currencies", h.adminListFXCurrencies)
		mux.HandleFunc("PUT /admin/fx/currencies/{currency}/policy", h.adminUpdateFXCurrencyPolicy)
		mux.HandleFunc("GET /admin/fx/pairs", h.adminListFXPairs)
		mux.HandleFunc("GET /admin/fx/positions", h.adminListFXPositions)
		mux.HandleFunc("GET /admin/fx/positions/daily", h.adminGetFXDailyPositionReport)
		mux.HandleFunc("GET /admin/fx/reconciliation", h.adminReconcileFXConversions)
		mux.HandleFunc("POST /admin/fx/rebalances", h.adminCreateFXRebalance)
		mux.HandleFunc("GET /admin/fx/rates", h.adminListFXRates)
		mux.HandleFunc("POST /admin/fx/rates", h.adminCreateFXRate)
		mux.HandleFunc("POST /admin/fx/rates/{id}/submit", h.adminSubmitFXRate)
		mux.HandleFunc("POST /admin/fx/rates/{id}/approve", h.adminApproveFXRate)
		mux.HandleFunc("POST /admin/fx/rates/{id}/reject", h.adminRejectFXRate)
		mux.HandleFunc("POST /admin/fx/rates/{id}/retire", h.adminRetireFXRate)
		mux.HandleFunc("PUT /admin/fx/pairs/{id}/controls", h.adminUpdateFXPairControls)
		mux.HandleFunc("PUT /admin/fx/pairs/{id}/status", h.adminUpdateFXPairStatus)
		mux.HandleFunc("PUT /admin/fx/directions/{id}/controls", h.adminUpdateFXDirectionControls)
		mux.HandleFunc("PUT /admin/fx/position-limits", h.adminUpdateFXPositionLimit)
		mux.HandleFunc("GET /admin/fx/quotes/{id}", h.adminGetFXQuote)
		mux.HandleFunc("GET /admin/fx/conversions/{id}", h.adminGetFXConversion)
	}

	return mux
}
