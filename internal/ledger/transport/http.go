package transport

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/feepolicy"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

const (
	defaultEntriesLimit = 50
	maxEntriesLimit     = 200
	// maxStatementDays caps a statement request's [from, to] span
	// (docs/plan/15 Task T2, decision K7) — checked here (cheap, no DB
	// touch) before calling the service, which separately enforces the
	// row-count cap (needs actual query results).
	maxStatementDays = 92
)

// adminOnlyTypes are transaction types that must never be triggered without
// an admin JWT — compliance/correction actions (docs/plan/05 Task 1b.4).
// Enforced on BOTH routers as defense in depth, even though the internal
// router is already network-isolated (docs/plan/10 Task T1).
//
// adjustment_credit/adjustment_debit are deliberately ABSENT here — as of
// docs/plan/16 Task T1, they aren't merely admin-gated, they are blocked
// from direct POST entirely (see directPostBlockedTypes below) and only
// reachable via the maker-checker /admin/adjustments flow.
var adminOnlyTypes = map[string]bool{
	"freeze_initiate":   true,
	"freeze_release":    true,
	"freeze_confiscate": true,
	"reversal":          true,
	"chargeback":        true,
}

// publicUserTypes are the ONLY transaction types reachable from the
// public-facing router (NewRouter). Everything else — money movement to/from
// system accounts (money_in, refund, withdraw settlement, escrow release,
// fee_collect) plus the adminOnlyTypes above — is only reachable via the
// internal router (NewInternalRouter, docs/plan/10 Task T1). This closes the
// hole where any authenticated user could credit their own cash from a
// settlement account with no real deposit behind it.
var publicUserTypes = map[string]bool{
	"transfer_p2p":      true,
	"transfer_pocket":   true,
	"withdraw_initiate": true,
	"escrow_hold":       true,
}

// NewRouter builds the public-facing HTTP handler for the ledger module. The
// caller (the composition root, internal/handler/router.go) is responsible
// for mounting it under a path prefix and wrapping it with auth/rate-limit
// middleware — this router assumes every request already carries a valid
// JWT. Only publicUserTypes are postable here; every other transaction type
// is rejected with 403. See NewInternalRouter for the unrestricted variant.
func NewRouter(svc Service) http.Handler {
	return NewRouterWithPolicy(svc, nil)
}

// NewRouterWithPolicy is NewRouter plus a policy engine (docs/plan/17 Task
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
// (docs/plan/37): every public-router posting is screened BEFORE svc.Post
// is called — before any DB transaction opens, unlike the old in-transaction
// PrePostHook seam this replaces. A nil fraudClient disables screening
// entirely (same "absent = no-op" convention as policy/feePolicy). A nil
// logger falls back to slog.Default(). feeQuoteTTL (docs/plan/38 Task T3)
// <=0 falls back to feepolicy.DefaultQuoteTTL.
func NewRouterWithFraud(svc Service, policy PolicyChecker, feePolicy *feepolicy.Policy, fraudClient *fraudcheck.Client, logger *slog.Logger, feeQuoteTTL time.Duration) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	h := &handler{svc: svc, allowedTypes: publicUserTypes, feePolicy: feePolicy, policy: policy, fraudClient: fraudClient, logger: logger, feeQuoteTTL: feeQuoteTTL}
	return h.mux()
}

// NewInternalRouter builds the HTTP handler meant for the internal-only
// listener (INTERNAL_APP_PORT, bound to 127.0.0.1 by default — see
// internal/handler/router.go NewInternalRouter and cmd/gateway/main.go). It
// accepts every registered transaction type — including money_in, refund,
// withdraw settlement, escrow release, fee_collect — because those are
// legitimately triggered by trusted internal callers (payment-gateway
// webhook handlers, ops tooling), never by an end user directly. Compliance
// actions (adminOnlyTypes) remain admin-gated even here.
func NewInternalRouter(svc Service) http.Handler {
	return NewInternalRouterWithFeePolicy(svc, nil)
}

func NewInternalRouterWithFeePolicy(svc Service, feePolicy *feepolicy.Policy) http.Handler {
	h := &handler{svc: svc, allowedTypes: nil, feePolicy: feePolicy}
	return h.mux()
}

func (h *handler) mux() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /transactions", h.postTransaction)
	mux.HandleFunc("GET /transactions/{id}", h.getTransaction)
	mux.HandleFunc("GET /accounts", h.listAccounts)
	mux.HandleFunc("GET /accounts/{id}/balance", h.getBalance)
	mux.HandleFunc("GET /accounts/{id}/entries", h.listEntries)
	mux.HandleFunc("GET /accounts/{id}/statement", h.getStatement)
	mux.HandleFunc("POST /accounts/pockets", h.createPocket)

	// Fee quotes (docs/plan/38 Task T3) — PUBLIC router only: this is the
	// "what will I pay" preview an end user requests before committing to a
	// transaction. Reachable automatically as
	// /api/v1/ledger/fees/quote via the existing gateway proxy, no gateway
	// change needed.
	if h.allowedTypes != nil {
		mux.HandleFunc("POST /fees/quote", h.createQuote)
	}

	// Scheduled transactions (docs/plan/19 Task T1) — on BOTH routers, a
	// user manages only their own schedules (ownership enforced in
	// internal/ledger/service/schedule), same as listAccounts/getBalance above.
	mux.HandleFunc("POST /schedules", h.createSchedule)
	mux.HandleFunc("GET /schedules", h.listSchedules)
	mux.HandleFunc("POST /schedules/{id}/pause", h.pauseSchedule)
	mux.HandleFunc("POST /schedules/{id}/resume", h.resumeSchedule)
	mux.HandleFunc("POST /schedules/{id}/cancel", h.cancelSchedule)

	// Admin outbox dead-letter replay (docs/plan/12 Task T3) — internal
	// router ONLY (allowedTypes == nil is this package's existing signal
	// for "this is the internal router", set by NewInternalRouter). Also
	// admin-gated inside the handlers themselves — defense in depth, since
	// network isolation of the internal listener is the primary control,
	// not the only one, for an operation this sensitive.
	if h.allowedTypes == nil {
		mux.HandleFunc("POST /admin/outbox/dead/{id}/replay", h.replayDeadEvent)
		mux.HandleFunc("POST /admin/outbox/dead/replay-all", h.replayAllDeadEvents)
		mux.HandleFunc("GET /admin/outbox/dead", h.listDeadEvents)

		// Maker-checker adjustment governance (docs/plan/16 Task T1) —
		// internal router only, admin-gated inside each handler. This is
		// the ONLY reachable path to adjustment_credit/adjustment_debit —
		// see the directPostBlockedTypes check in postTransaction.
		mux.HandleFunc("POST /admin/adjustments", h.createAdjustment)
		mux.HandleFunc("POST /admin/adjustments/{id}/approve", h.approveAdjustment)
		mux.HandleFunc("POST /admin/adjustments/{id}/reject", h.rejectAdjustment)
		mux.HandleFunc("GET /admin/adjustments", h.listAdjustments)
		mux.HandleFunc("GET /admin/adjustments/{id}", h.getAdjustment)

		// External reconciliation (docs/plan/16 Task T2) — internal router
		// only, admin-gated. Resolve creates a pending adjustment via the
		// same maker-checker path above; it never moves money by itself.
		mux.HandleFunc("POST /admin/recon/batches", h.createReconBatch)
		mux.HandleFunc("GET /admin/recon/batches", h.listReconBatches)
		mux.HandleFunc("GET /admin/recon/batches/{id}", h.getReconBatch)
		mux.HandleFunc("POST /admin/recon/items/{id}/resolve", h.resolveReconItem)

		// Schedule runner ops/testing trigger (docs/plan/19 Task T1 step 5)
		// — internal router only, admin-gated.
		mux.HandleFunc("POST /admin/schedules/run", h.runSchedulesNow)

		// Batch disbursement (docs/plan/19 Task T2) — internal router only,
		// admin-gated.
		mux.HandleFunc("POST /admin/disbursements", h.createDisbursementBatch)
		mux.HandleFunc("POST /admin/disbursements/{id}/run", h.runDisbursement)
		mux.HandleFunc("GET /admin/disbursements/{id}", h.getDisbursementReport)

		// Interest accrual (docs/plan/19 Task T3) — internal router only,
		// admin-gated.
		mux.HandleFunc("PUT /admin/savings/{account_id}", h.setSavingsConfig)
		mux.HandleFunc("GET /admin/savings", h.listSavingsConfigs)

		mux.HandleFunc("GET /admin/reports/{kind}", h.getReport)

		// Database-driven fee management (docs/plan/33 Task T3). Disable is
		// represented by enabled=false so pricing history remains auditable.
		mux.HandleFunc("GET /admin/ledger/fee-rules", h.listFeeRules)
		mux.HandleFunc("POST /admin/ledger/fee-rules", h.createFeeRule)
		mux.HandleFunc("PUT /admin/ledger/fee-rules/{id}", h.updateFeeRule)
	}

	return mux
}

type transactionTypeValidator interface {
	IsKnownTransactionType(string) bool
}

func (h *handler) validateFeeRuleRequest(w http.ResponseWriter, req feeRuleRequest) (*uuid.UUID, bool) {
	registry, ok := h.svc.(transactionTypeValidator)
	if !ok || !registry.IsKnownTransactionType(req.TxType) {
		response.BadRequest(w, "tx_type is not registered")
		return nil, false
	}
	if req.Gateway != "" && !constant.ValidGateways[req.Gateway] {
		response.BadRequest(w, "gateway is not registered")
		return nil, false
	}
	if !currency.IsValid(req.Currency) {
		response.BadRequest(w, "currency is not registered")
		return nil, false
	}
	if req.FlatMinorUnits < 0 {
		response.BadRequest(w, "flat_minor_units must not be negative")
		return nil, false
	}
	if req.PercentBasisPts < 0 || req.PercentBasisPts >= 10_000 {
		response.BadRequest(w, "percent_basis_pts must be in [0, 10000)")
		return nil, false
	}
	if req.FeeGateway == "" {
		req.FeeGateway = "platform"
	}
	if !constant.ValidGateways[req.FeeGateway] {
		response.BadRequest(w, "fee_gateway is not registered")
		return nil, false
	}
	if req.UserID == "" {
		return nil, true
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		response.BadRequest(w, "user_id must be a valid UUID")
		return nil, false
	}
	return &userID, true
}

func (h *handler) listFeeRules(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	rules, err := h.feePolicy.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]feeRuleResponse, len(rules))
	for i, rule := range rules {
		out[i] = toFeeRuleResponse(rule)
	}
	response.OK(w, map[string]any{"fee_rules": out})
}

func (h *handler) createFeeRule(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	var req feeRuleRequest
	if !response.Decode(w, r, &req) {
		return
	}
	userID, ok := h.validateFeeRuleRequest(w, req)
	if !ok {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	feeGateway := req.FeeGateway
	if feeGateway == "" {
		feeGateway = "platform"
	}
	rule, err := h.feePolicy.Create(r.Context(), feepolicy.Rule{
		TxType: req.TxType, Gateway: req.Gateway, Currency: req.Currency, UserID: userID,
		FlatMinorUnits: req.FlatMinorUnits, PercentBasisPts: req.PercentBasisPts,
		FeeGateway: feeGateway, Enabled: enabled,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, toFeeRuleResponse(rule))
}

func (h *handler) updateFeeRule(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid fee rule id")
		return
	}
	var req feeRuleRequest
	if !response.Decode(w, r, &req) {
		return
	}
	userID, ok := h.validateFeeRuleRequest(w, req)
	if !ok {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	feeGateway := req.FeeGateway
	if feeGateway == "" {
		feeGateway = "platform"
	}
	rule, err := h.feePolicy.Update(r.Context(), feepolicy.Rule{
		ID: id, TxType: req.TxType, Gateway: req.Gateway, Currency: req.Currency, UserID: userID,
		FlatMinorUnits: req.FlatMinorUnits, PercentBasisPts: req.PercentBasisPts,
		FeeGateway: feeGateway, Enabled: enabled,
	})
	if errors.Is(err, feepolicy.ErrRuleNotFound) {
		response.NotFound(w, "fee rule not found")
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toFeeRuleResponse(rule))
}

// createQuote serves POST /fees/quote (docs/plan/38 Task T3, public router
// only) — user_id always comes from the JWT, never the request body, same
// principle as postTransaction's own userID handling. Topup (money_in) is
// explicitly quotable even though it can't be POSTed directly here — a
// quote never moves money, so allowing it doesn't reopen the money_in
// direct-post hole publicUserTypes/allowedTypes protects against.
func (h *handler) createQuote(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	var req quoteRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if registry, rok := h.svc.(transactionTypeValidator); !rok || !registry.IsKnownTransactionType(req.TransactionType) {
		response.BadRequest(w, "transaction_type is not registered")
		return
	}
	if req.Gateway != "" && !constant.ValidGateways[req.Gateway] {
		response.BadRequest(w, "gateway is not registered")
		return
	}
	amount, err := decimalFromString(req.Amount)
	if err != nil {
		if errors.Is(err, errNonIntegralAmount) {
			response.BadRequest(w, err.Error())
		} else {
			response.BadRequest(w, "amount must be a valid decimal string")
		}
		return
	}
	if !amount.IsPositive() {
		response.BadRequest(w, "amount must be positive")
		return
	}
	if h.feePolicy == nil {
		response.InternalServerError(w, errors.New("fee quotes are not configured"))
		return
	}

	quoteCurrency := req.Currency
	if quoteCurrency == "" {
		resolved, cerr := h.svc.GetUserCurrency(r.Context(), userID, "")
		if cerr == nil {
			quoteCurrency = resolved
		}
	}

	q, err := h.feePolicy.CreateQuote(r.Context(), userID, req.TransactionType, req.Gateway, quoteCurrency, amount, h.feeQuoteTTL)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.Created(w, toQuoteResponse(q))
}

// directPostBlockedTypes are transaction types that must NEVER be posted
// directly through POST /transactions, on either router — the ONLY path to
// them is the maker-checker adjustment flow (docs/plan/16 Task T1, decision
// K8). Distinct from adminOnlyTypes: those are gated by role, these are
// gated out of existence on this endpoint entirely, admin or not.
var directPostBlockedTypes = map[string]bool{
	"adjustment_credit":          true,
	"adjustment_debit":           true,
	"adjustment_suspense_credit": true,
	"adjustment_suspense_debit":  true,
}

type handler struct {
	svc Service
	// allowedTypes restricts which transaction types postTransaction accepts.
	// nil means every registered type is allowed (internal router);
	// non-nil is an explicit allowlist (public router).
	allowedTypes map[string]bool
	// feePolicy computes server-side fees for the public router
	// (docs/plan/10 Task T3) — see buildMetadata in metadata.go.
	feePolicy *feepolicy.Policy
	// policy evaluates per-user/per-type limits (docs/plan/17 Task T1)
	// before postTransaction calls svc.Post. nil on the internal router —
	// trusted internal callers (payment-gateway webhooks, ops tooling) are
	// not subject to end-user velocity limits.
	policy PolicyChecker
	// fraudClient screens a candidate transaction BEFORE any DB work
	// (docs/plan/37) — nil on the internal router (disbursement/adjustment/
	// system postings are not user-flow screening targets) and nil-safe on
	// the public router too (screening simply skipped if never configured,
	// same "absent = no-op" convention as feePolicy/policy above).
	fraudClient *fraudcheck.Client
	logger      *slog.Logger
	// feeQuoteTTL is how long a quote created via POST /fees/quote
	// (docs/plan/38 Task T3) stays consumable. <=0 falls back to
	// feepolicy.DefaultQuoteTTL.
	feeQuoteTTL time.Duration
}

// PolicyChecker is satisfied structurally by internal/policy.Engine —
// defined here, not imported from there, so the ledger module never
// depends on internal/policy (docs/plan/17 Task T1, decision K-S S1:
// "ledger module tidak tahu-menahu").
type PolicyChecker interface {
	// Check reports whether userID may post a txType transaction of the
	// given amount. allowed=false means reject; rule names which limit
	// dimension was violated, detail is a human-readable message.
	Check(ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal) (allowed bool, rule string, detail string, err error)
	// Record registers a transaction that already posted successfully —
	// callers must NEVER call this for a transaction that failed to post.
	Record(ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal)
}

// currentUserID extracts and parses the authenticated user's ID from the JWT
// claims already validated by pkg/middleware.WithAuth.
func currentUserID(r *http.Request) (uuid.UUID, bool) {
	raw := middleware.UserIDFromCtx(r.Context())
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func isAdmin(r *http.Request) bool {
	claims := middleware.GetClaims(r.Context())
	return claims != nil && claims.Role == "admin"
}

func (h *handler) postTransaction(w http.ResponseWriter, r *http.Request) {
	if h.allowedTypes != nil {
		claims := middleware.GetClaims(r.Context())
		if claims == nil || claims.KYCLevel < 1 {
			response.JSON(w, http.StatusForbidden, map[string]any{"code": "KYC_REQUIRED", "min_level": 1})
			return
		}
	}
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}

	var req postTransactionRequest
	if !response.Decode(w, r, &req) {
		return
	}

	if len(req.IdempotencyKey) < 8 || len(req.IdempotencyKey) > 128 {
		response.BadRequest(w, "idempotency_key must be between 8 and 128 characters")
		return
	}
	if req.Type == "" {
		response.BadRequest(w, "type is required")
		return
	}
	if h.allowedTypes != nil && !h.allowedTypes[req.Type] {
		response.Forbidden(w, "this transaction type is not available on the public API")
		return
	}
	if directPostBlockedTypes[req.Type] {
		response.Forbidden(w, "this transaction type cannot be posted directly — use POST /admin/adjustments (requires a second identity to approve)")
		return
	}
	if adminOnlyTypes[req.Type] && !isAdmin(r) {
		response.Forbidden(w, "this transaction type requires admin privileges")
		return
	}

	amount, err := decimalFromString(req.Amount)
	if err != nil {
		if errors.Is(err, errNonIntegralAmount) {
			response.BadRequest(w, err.Error())
		} else {
			response.BadRequest(w, "amount must be a valid decimal string")
		}
		return
	}

	// Idempotency scope defaults to the caller's own userID — this is the
	// ONLY behavior on the public router (allowedTypes != nil): a client
	// cannot spoof another user's scope or collide with their keys. The
	// internal router (allowedTypes == nil, trusted caller) may pass an
	// explicit scope — e.g. a payment-gateway webhook handler scoping by
	// provider transaction id rather than by end-user (docs/plan/10 T2).
	idemScope := userID.String()
	if h.allowedTypes == nil && req.IdempotencyScope != "" {
		idemScope = req.IdempotencyScope
	}

	// Metadata is never passed through verbatim on the public router — see
	// buildMetadata (metadata.go, docs/plan/10 Task T3): gateway is
	// validated, fee_amount/fee_gateway are stripped and replaced with a
	// server-computed fee, and only a small set of descriptive keys survive.
	metadata, err := h.buildMetadata(r.Context(), userID, req, amount)
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}

	cmd := processors.Command{
		IdempotencyKey:   req.IdempotencyKey,
		IdempotencyScope: idemScope,
		Type:             req.Type,
		Amount:           amount,
		UserID:           userID,
		PocketCode:       req.PocketCode,
		Metadata:         metadata,
		QuoteID:          req.QuoteID,
	}
	if req.TargetUserID != "" {
		targetID, err := uuid.Parse(req.TargetUserID)
		if err != nil {
			response.BadRequest(w, "target_user_id must be a valid UUID")
			return
		}
		cmd.TargetUserID = targetID
	}
	if req.ReferenceID != "" {
		refID, err := uuid.Parse(req.ReferenceID)
		if err != nil {
			response.BadRequest(w, "reference_id must be a valid UUID")
			return
		}
		cmd.ReferenceID = refID
	}

	if h.policy != nil {
		allowed, rule, detail, err := h.policy.Check(r.Context(), userID, req.Type, amount)
		if err != nil {
			response.InternalServerError(w, err)
			return
		}
		if !allowed {
			response.UnprocessableEntity(w, fmt.Sprintf("policy limit exceeded (%s): %s", rule, detail))
			return
		}
	}

	// AML/fraud screening (docs/plan/37) — runs on the PUBLIC router ONLY,
	// same layer as the policy check above, BEFORE svc.Post ever opens a DB
	// transaction. This replaces the old in-transaction PrePostHook seam
	// (docs/plan/20), which held a FOR UPDATE row lock for up to 500ms per
	// posting waiting on this exact same network round-trip. h.allowedTypes
	// is nil on the internal router (disbursement/adjustment/system
	// postings) — screening is a user-flow control, not applied there.
	if h.fraudClient != nil && h.allowedTypes != nil {
		screenCurrency, cerr := h.svc.GetUserCurrency(r.Context(), userID, req.PocketCode)
		if cerr != nil {
			screenCurrency = ""
		}
		verdict, ferr := h.fraudClient.Check(r.Context(), "p2p_transfer", req.Type, userID, amount, screenCurrency)
		if ferr != nil {
			if errors.Is(ferr, fraudcheck.ErrDependencyUnavailable) {
				// docs/plan/45 Task T3/K4: fraud-service is reachable but its
				// velocity dependency is down — fail CLOSED, unlike every
				// other Check error below (fail open). No posting has
				// happened yet.
				h.logger.Warn("screening dependency unavailable, failing closed", "type", req.Type)
				response.ServiceUnavailable(w, "DEPENDENCY_UNAVAILABLE", "fraud screening dependency unavailable")
				return
			}
			h.logger.Error("screening check error, failing open", "error", ferr, "type", req.Type)
		} else if verdict.Block {
			writeError(w, apperror.NewBizErr(apperror.ErrScreeningBlocked, verdict.Reason))
			return
		}
	}

	if err := h.svc.Post(r.Context(), cmd); err != nil {
		writeError(w, err)
		return
	}

	// Record AFTER Post succeeds — a failed posting must never consume quota
	// (docs/plan/17 Task T1 step 2).
	if h.policy != nil {
		h.policy.Record(r.Context(), userID, req.Type, amount)
	}

	response.Created(w, postTransactionResponse{Status: "posted", IdempotencyKey: req.IdempotencyKey})
}

func (h *handler) getTransaction(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	txID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid transaction id")
		return
	}

	if !isAdmin(r) {
		allowed, err := h.svc.CanAccessTransaction(r.Context(), txID, userID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !allowed {
			response.NotFound(w, "transaction not found")
			return
		}
	}

	tx, err := h.svc.GetTransaction(r.Context(), txID)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toTransactionResponse(tx))
}

func (h *handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	accounts, err := h.svc.ListAccounts(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]accountResponse, len(accounts))
	for i, a := range accounts {
		out[i] = toAccountResponse(a)
	}
	response.OK(w, out)
}

func (h *handler) getBalance(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	accountID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid account id")
		return
	}

	if !isAdmin(r) {
		allowed, err := h.svc.CanAccessAccount(r.Context(), accountID, userID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !allowed {
			response.NotFound(w, "account not found")
			return
		}
	}

	asOfRaw := r.URL.Query().Get("as_of")
	if asOfRaw == "" {
		bal, err := h.svc.GetBalance(r.Context(), accountID)
		if err != nil {
			writeError(w, err)
			return
		}
		response.OK(w, toBalanceResponse(bal))
		return
	}

	asOf, err := time.Parse("2006-01-02", asOfRaw)
	if err != nil {
		response.BadRequest(w, "as_of must be YYYY-MM-DD")
		return
	}
	bal, err := h.svc.GetBalanceAsOf(r.Context(), accountID, asOf)
	if err != nil {
		writeError(w, err)
		return
	}
	out := toBalanceResponse(bal)
	out.AsOf = asOfRaw
	response.OK(w, out)
}

func (h *handler) listEntries(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	accountID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid account id")
		return
	}

	if !isAdmin(r) {
		allowed, err := h.svc.CanAccessAccount(r.Context(), accountID, userID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !allowed {
			response.NotFound(w, "account not found")
			return
		}
	}

	limit := defaultEntriesLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			response.BadRequest(w, "limit must be a positive integer")
			return
		}
		limit = min(parsed, maxEntriesLimit)
	}

	beforeCreatedAt, beforeID, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}

	entries, err := h.svc.ListEntries(r.Context(), accountID, beforeCreatedAt, beforeID, limit)
	if err != nil {
		writeError(w, err)
		return
	}

	out := listEntriesResponse{Entries: make([]entryResponse, len(entries))}
	for i, e := range entries {
		out.Entries[i] = toEntryResponse(e)
	}
	if len(entries) == limit {
		last := entries[len(entries)-1]
		out.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	response.OK(w, out)
}

// getStatement handles GET /accounts/{id}/statement?from=&to=&format=
// (docs/plan/15 Task T2). from/to are inclusive Asia/Jakarta calendar dates.
// format defaults to json; csv streams row-by-row rather than buffering the
// whole (up to 5,000-row) body in memory.
func (h *handler) getStatement(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	accountID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid account id")
		return
	}

	if !isAdmin(r) {
		allowed, err := h.svc.CanAccessAccount(r.Context(), accountID, userID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !allowed {
			response.NotFound(w, "account not found")
			return
		}
	}

	fromRaw := r.URL.Query().Get("from")
	toRaw := r.URL.Query().Get("to")
	if fromRaw == "" || toRaw == "" {
		response.BadRequest(w, "from and to are required (YYYY-MM-DD)")
		return
	}
	from, err := time.Parse("2006-01-02", fromRaw)
	if err != nil {
		response.BadRequest(w, "from must be YYYY-MM-DD")
		return
	}
	to, err := time.Parse("2006-01-02", toRaw)
	if err != nil {
		response.BadRequest(w, "to must be YYYY-MM-DD")
		return
	}
	if from.After(to) {
		response.BadRequest(w, "from must not be after to")
		return
	}
	if to.Sub(from) > maxStatementDays*24*time.Hour {
		response.BadRequest(w, fmt.Sprintf("range too large, narrow the period (max %d days)", maxStatementDays))
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		response.BadRequest(w, "format must be 'json' or 'csv'")
		return
	}

	stmt, err := h.svc.Statement(r.Context(), accountID, from, to)
	if err != nil {
		writeError(w, err)
		return
	}

	if format == "csv" {
		writeStatementCSV(w, stmt)
		return
	}
	response.OK(w, toStatementResponse(stmt))
}

// writeStatementCSV streams the statement row by row — never buffers the
// full (up to 5,000-row) body in memory (docs/plan/15 Task T2, decision K7,
// box-size constraint). encoding/csv handles RFC 4180 escaping.
func writeStatementCSV(w http.ResponseWriter, stmt model.Statement) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(
		"attachment; filename=statement_%s_%s_%s.csv",
		stmt.AccountID, stmt.From.Format("2006-01-02"), stmt.To.Format("2006-01-02")))
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"entry_id", "tx_id", "type", "direction", "amount", "balance_after", "note", "created_at"})
	for _, e := range stmt.Entries {
		_ = cw.Write([]string{
			e.ID.String(), e.TransactionID.String(), e.TransactionType, string(e.Direction),
			e.Amount.String(), e.BalanceAfter.String(), e.Note, e.CreatedAt.Format(time.RFC3339),
		})
	}
	cw.Flush()
}

func (h *handler) createPocket(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}

	var req createPocketRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.Currency == "" {
		response.BadRequest(w, "currency is required")
		return
	}
	if req.PocketCode == "" {
		response.BadRequest(w, "pocket_code is required")
		return
	}

	acc, err := h.svc.CreatePocket(r.Context(), userID, req.Currency, req.PocketCode)
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, toAccountResponse(acc))
}

// ─── Admin: outbox dead-letter replay (docs/plan/12 Task T3) ──────────────────
//
// Both handlers are only ever registered on the internal router (see mux)
// but are admin-gated here too as defense in depth — this is a sensitive,
// infrequent operation and network isolation alone is not the only control
// worth having for it.

func (h *handler) replayDeadEvent(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid event id")
		return
	}

	if err := h.svc.ReplayDeadEvent(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, replayDeadEventResponse{Replayed: true})
}

func (h *handler) replayAllDeadEvents(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}

	var req replayAllDeadRequest
	if !response.Decode(w, r, &req) {
		return
	}

	olderThan := time.Now()
	if req.OlderThan != "" {
		parsed, err := time.Parse(time.RFC3339, req.OlderThan)
		if err != nil {
			response.BadRequest(w, "older_than must be an RFC3339 timestamp")
			return
		}
		olderThan = parsed
	}

	n, err := h.svc.ReplayDeadEvents(r.Context(), olderThan)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, replayAllDeadResponse{ReplayedCount: n})
}

// listDeadEvents serves GET /admin/outbox/dead?limit=&offset= (docs/plan/25
// Task T5) — lets an operator see what needs replay without querying
// Postgres directly.
func (h *handler) listDeadEvents(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}

	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			response.BadRequest(w, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			response.BadRequest(w, "offset must be a non-negative integer")
			return
		}
		offset = parsed
	}

	events, err := h.svc.ListDeadOutboxEvents(r.Context(), limit, offset)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]deadOutboxEventResponse, len(events))
	for i, e := range events {
		out[i] = toDeadOutboxEventResponse(e)
	}
	response.OK(w, listDeadOutboxEventsResponse{Events: out})
}

// ─── Maker-checker adjustments (docs/plan/16 Task T1) ──────────────────────
// Internal router only, admin-gated in every handler — defense in depth on
// top of network isolation, same pattern as the outbox replay endpoints
// above. This is the ONLY reachable path to adjustment_credit/
// adjustment_debit (see directPostBlockedTypes in postTransaction).

func (h *handler) createAdjustment(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	requestedBy, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}

	var req createAdjustmentRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.Type == "" {
		response.BadRequest(w, "type is required")
		return
	}
	amount, err := decimalFromString(req.Amount)
	if err != nil {
		if errors.Is(err, errNonIntegralAmount) {
			response.BadRequest(w, err.Error())
		} else {
			response.BadRequest(w, "amount must be a valid decimal string")
		}
		return
	}
	// user_id is required for adjustment_credit/debit but absent for
	// adjustment_suspense_credit/debit (docs/plan/16 Task T2), which target
	// a gateway's suspense account via req.Metadata["gateway"] instead —
	// svc.CreateAdjustment enforces which one is actually required per type.
	var targetUserID uuid.UUID
	if req.UserID != "" {
		targetUserID, err = uuid.Parse(req.UserID)
		if err != nil {
			response.BadRequest(w, "user_id must be a valid UUID")
			return
		}
	}
	if req.Reason == "" {
		response.BadRequest(w, "reason is required")
		return
	}

	id, err := h.svc.CreateAdjustment(r.Context(), requestedBy.String(), req.Type, amount, targetUserID, req.Metadata, req.Reason)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, createAdjustmentResponse{ID: id})
}

func (h *handler) approveAdjustment(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	approverID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid adjustment id")
		return
	}

	txID, err := h.svc.ApproveAdjustment(r.Context(), id, approverID.String())
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, approveAdjustmentResponse{ExecutedTxID: txID})
}

func (h *handler) rejectAdjustment(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	approverID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid adjustment id")
		return
	}

	if err := h.svc.RejectAdjustment(r.Context(), id, approverID.String()); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, rejectAdjustmentResponse{Rejected: true})
}

func (h *handler) getAdjustment(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid adjustment id")
		return
	}

	pa, err := h.svc.GetAdjustment(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toAdjustmentResponse(pa))
}

func (h *handler) listAdjustments(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}

	status := r.URL.Query().Get("status")
	var limit int
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			response.BadRequest(w, "limit must be a positive integer")
			return
		}
		limit = parsed
	}

	list, err := h.svc.ListAdjustments(r.Context(), status, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]adjustmentResponse, len(list))
	for i, a := range list {
		out[i] = toAdjustmentResponse(a)
	}
	response.OK(w, listAdjustmentsResponse{Adjustments: out})
}

// ─── External reconciliation (docs/plan/16 Task T2) ────────────────────────
// Internal router only, admin-gated in every handler — same defense-in-depth
// pattern as the adjustment endpoints above. resolveReconItem creates a
// pending adjustment via the maker-checker path; it never moves money by
// itself (decision K5 step 5).

// maxReconCSVUploadBytes caps the multipart request body — a 50,000-row CSV
// (the service-layer row cap, docs/plan/16 Task T2 step 3) at a generous
// worst-case row width fits well under this; a request over the limit is
// rejected outright rather than partially read.
const maxReconCSVUploadBytes = 10 << 20 // 10MiB

// maxReconCSVRows mirrors service/recon's own authoritative cap — checked
// again here so an oversized file is rejected while STREAMING the CSV
// (never fully buffered in memory) rather than only after every row has
// already been parsed and handed to the service layer.
const maxReconCSVRows = 50_000

func (h *handler) createReconBatch(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	createdBy, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxReconCSVUploadBytes)
	if err := r.ParseMultipartForm(maxReconCSVUploadBytes); err != nil {
		response.BadRequest(w, "invalid multipart form or file too large")
		return
	}

	gateway := r.FormValue("gateway")
	if gateway == "" || !constant.ValidGateways[gateway] {
		response.BadRequest(w, "gateway is required and must be a known gateway")
		return
	}
	reportDate, err := time.Parse("2006-01-02", r.FormValue("report_date"))
	if err != nil {
		response.BadRequest(w, "report_date is required and must be YYYY-MM-DD")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		response.BadRequest(w, "file is required (multipart field 'file', CSV columns: external_ref,amount,settled_at)")
		return
	}
	defer file.Close()

	rows, err := parseReconCSV(file)
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}

	batchID, err := h.svc.ImportReconBatch(r.Context(), gateway, reportDate, header.Filename, rows, createdBy.String())
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, createReconBatchResponse{ID: batchID})
}

// parseReconCSV streams external_ref,amount,settled_at rows — header order
// is flexible (matched by name), amount is validated integral minor-unit
// (same rule as every other amount in this API, docs/plan/10 Task T4).
func parseReconCSV(r io.Reader) ([]model.ReconImportRow, error) {
	cr := csv.NewReader(r)
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	col := make(map[string]int, len(header))
	for i, name := range header {
		col[strings.TrimSpace(strings.ToLower(name))] = i
	}
	for _, want := range []string{"external_ref", "amount", "settled_at"} {
		if _, ok := col[want]; !ok {
			return nil, fmt.Errorf("CSV missing required column %q", want)
		}
	}

	var rows []model.ReconImportRow
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("malformed CSV row %d: %w", len(rows)+2, err)
		}
		if len(rows) >= maxReconCSVRows {
			return nil, fmt.Errorf("CSV has more than %d rows — split the file", maxReconCSVRows)
		}
		amount, err := decimalFromString(rec[col["amount"]])
		if err != nil {
			return nil, fmt.Errorf("row %d (external_ref=%q): %w", len(rows)+2, rec[col["external_ref"]], err)
		}
		rows = append(rows, model.ReconImportRow{
			ExternalRef: rec[col["external_ref"]],
			Amount:      amount,
			SettledAt:   rec[col["settled_at"]],
		})
	}
	return rows, nil
}

func (h *handler) getReconBatch(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	batchID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid batch id")
		return
	}

	matchStatus := r.URL.Query().Get("match_status")
	var limit, offset int
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			response.BadRequest(w, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			response.BadRequest(w, "offset must be a non-negative integer")
			return
		}
		offset = parsed
	}

	report, err := h.svc.GetReconBatchReport(r.Context(), batchID, matchStatus, limit, offset)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toReconBatchReportResponse(report))
}

// listReconBatches serves GET /admin/recon/batches?limit=&offset=
// (docs/plan/25 Task T5) — lets an operator find a batch's id without SQL
// before drilling into GET /admin/recon/batches/{id}.
func (h *handler) listReconBatches(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}

	limit, offset := 0, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			response.BadRequest(w, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			response.BadRequest(w, "offset must be a non-negative integer")
			return
		}
		offset = parsed
	}

	batches, err := h.svc.ListReconBatches(r.Context(), limit, offset)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]reconBatchResponse, len(batches))
	for i, b := range batches {
		out[i] = toReconBatchListResponse(b)
	}
	response.OK(w, listReconBatchesResponse{Batches: out})
}

func (h *handler) resolveReconItem(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	requestedBy, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	itemID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid recon item id")
		return
	}

	var req resolveReconItemRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.Type == "" {
		response.BadRequest(w, "type is required (adjustment_suspense_credit or adjustment_suspense_debit)")
		return
	}
	var amount decimal.Decimal
	if req.Amount != "" {
		amount, err = decimalFromString(req.Amount)
		if err != nil {
			if errors.Is(err, errNonIntegralAmount) {
				response.BadRequest(w, err.Error())
			} else {
				response.BadRequest(w, "amount must be a valid decimal string")
			}
			return
		}
	}

	adjustmentID, err := h.svc.ResolveReconItem(r.Context(), itemID, requestedBy.String(), req.Type, amount, req.Reason)
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, resolveReconItemResponse{AdjustmentID: adjustmentID})
}

// ─── Scheduled transactions (docs/plan/19 Task T1) ──────────────────────────
// createSchedule/listSchedules/pauseSchedule/resumeSchedule/cancelSchedule
// are on the PUBLIC router — a user manages only their own schedules
// (ownership enforced in internal/ledger/service/schedule, same
// "not found rather than forbidden" reasoning as CanAccessAccount).
// runSchedulesNow is internal-router-only, admin-gated.

func (h *handler) createSchedule(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}

	var req createScheduleRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.Type == "" {
		response.BadRequest(w, "type is required")
		return
	}
	amount, err := decimalFromString(req.Amount)
	if err != nil {
		if errors.Is(err, errNonIntegralAmount) {
			response.BadRequest(w, err.Error())
		} else {
			response.BadRequest(w, "amount must be a valid decimal string")
		}
		return
	}
	var targetUserID uuid.UUID
	if req.TargetUserID != "" {
		targetUserID, err = uuid.Parse(req.TargetUserID)
		if err != nil {
			response.BadRequest(w, "invalid target_user_id")
			return
		}
	}
	if req.RunAtDate == "" {
		response.BadRequest(w, "run_at_date is required (YYYY-MM-DD)")
		return
	}
	runAtDate, err := time.Parse("2006-01-02", req.RunAtDate)
	if err != nil {
		response.BadRequest(w, "run_at_date must be YYYY-MM-DD")
		return
	}

	id, err := h.svc.CreateSchedule(r.Context(), userID, req.Type, amount, targetUserID, req.PocketCode, req.Metadata,
		req.ScheduleKind, runAtDate, req.DayOfMonth, userID.String())
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, createScheduleResponse{ID: id})
}

func (h *handler) listSchedules(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}

	list, err := h.svc.ListSchedules(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]scheduleResponse, len(list))
	for i, st := range list {
		out[i] = toScheduleResponse(st)
	}
	response.OK(w, listSchedulesResponse{Schedules: out})
}

func (h *handler) pauseSchedule(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid schedule id")
		return
	}
	if err := h.svc.PauseSchedule(r.Context(), id, userID); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, pauseScheduleResponse{Paused: true})
}

func (h *handler) resumeSchedule(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid schedule id")
		return
	}
	if err := h.svc.ResumeSchedule(r.Context(), id, userID); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, resumeScheduleResponse{Resumed: true})
}

func (h *handler) cancelSchedule(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid schedule id")
		return
	}
	if err := h.svc.CancelSchedule(r.Context(), id, userID); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, cancelScheduleResponse{Cancelled: true})
}

// runSchedulesNow is an ops/testing trigger for the daily schedule runner
// (docs/plan/19 Task T1 step 5) — internal router only, admin-gated. date
// defaults to today (Asia/Jakarta) if omitted.
func (h *handler) runSchedulesNow(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	asOf := time.Now()
	if raw := r.URL.Query().Get("date"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			response.BadRequest(w, "date must be YYYY-MM-DD")
			return
		}
		asOf = parsed
	}

	executed, failed, err := h.svc.RunSchedulesNow(r.Context(), asOf)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, runSchedulesResponse{Executed: executed, Failed: failed})
}

// ─── Batch disbursement (docs/plan/19 Task T2) ──────────────────────────────
// Internal router only, admin-gated in every handler — same defense-in-depth
// pattern as recon/adjustments above. Import only persists items ('pending');
// Run is the ONLY execution path, called repeatedly (by ops/a script) until
// Done — there is no separate "resume" endpoint, since calling Run again
// after a partial run already only re-selects items still pending/failed.

func (h *handler) createDisbursementBatch(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	createdBy, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxReconCSVUploadBytes)
	if err := r.ParseMultipartForm(maxReconCSVUploadBytes); err != nil {
		response.BadRequest(w, "invalid multipart form or file too large")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		response.BadRequest(w, "file is required (multipart field 'file', CSV columns: user_id,amount,note)")
		return
	}
	defer file.Close()

	rows, err := parseDisbursementCSV(file)
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}

	batchID, err := h.svc.ImportDisbursementBatch(r.Context(), header.Filename, rows, createdBy.String())
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, createDisbursementBatchResponse{ID: batchID})
}

// parseDisbursementCSV streams user_id,amount,note rows — header order is
// flexible (matched by name), amount is validated integral minor-unit
// before it ever reaches the service layer, and "note" is optional.
func parseDisbursementCSV(r io.Reader) ([]model.DisbursementImportRow, error) {
	cr := csv.NewReader(r)
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	col := make(map[string]int, len(header))
	for i, name := range header {
		col[strings.TrimSpace(strings.ToLower(name))] = i
	}
	for _, want := range []string{"user_id", "amount"} {
		if _, ok := col[want]; !ok {
			return nil, fmt.Errorf("CSV missing required column %q", want)
		}
	}
	noteIdx, hasNote := col["note"]

	var rows []model.DisbursementImportRow
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("malformed CSV row %d: %w", len(rows)+2, err)
		}
		if len(rows) >= maxReconCSVRows {
			return nil, fmt.Errorf("CSV has more than %d rows — split the file", maxReconCSVRows)
		}
		userID, err := uuid.Parse(rec[col["user_id"]])
		if err != nil {
			return nil, fmt.Errorf("row %d: invalid user_id %q", len(rows)+2, rec[col["user_id"]])
		}
		amount, err := decimalFromString(rec[col["amount"]])
		if err != nil {
			return nil, fmt.Errorf("row %d (user_id=%s): %w", len(rows)+2, userID, err)
		}
		note := ""
		if hasNote {
			note = rec[noteIdx]
		}
		rows = append(rows, model.DisbursementImportRow{UserID: userID, Amount: amount, Note: note})
	}
	return rows, nil
}

func (h *handler) runDisbursement(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	batchID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid batch id")
		return
	}
	retryFailed := r.URL.Query().Get("retry_failed") == "true"

	result, err := h.svc.RunDisbursement(r.Context(), batchID, retryFailed)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toRunDisbursementResponse(result))
}

func (h *handler) getDisbursementReport(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	batchID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid batch id")
		return
	}
	status := r.URL.Query().Get("status")
	limit, offset := 0, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			response.BadRequest(w, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			response.BadRequest(w, "offset must be a non-negative integer")
			return
		}
		offset = parsed
	}

	report, err := h.svc.GetDisbursementReport(r.Context(), batchID, status, limit, offset)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toDisbursementBatchReportResponse(report))
}

// ─── Interest accrual (docs/plan/19 Task T3) ────────────────────────────────
// Internal router only, admin-gated — same defense-in-depth pattern as
// every other admin surface above.

func (h *handler) setSavingsConfig(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	accountID, err := uuid.Parse(r.PathValue("account_id"))
	if err != nil {
		response.BadRequest(w, "invalid account id")
		return
	}

	var req setSavingsConfigRequest
	if !response.Decode(w, r, &req) {
		return
	}

	if err := h.svc.SetSavingsConfig(r.Context(), accountID, req.AnnualRateBps, req.Enabled); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, setSavingsConfigResponse{Set: true})
}

func (h *handler) listSavingsConfigs(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}

	configs, err := h.svc.ListSavingsConfigs(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]savingsConfigResponse, len(configs))
	for i, cfg := range configs {
		out[i] = toSavingsConfigResponse(cfg)
	}
	response.OK(w, listSavingsConfigsResponse{Configs: out})
}

// ─── AML/fraud screening (docs/plan/20 Task T1) ─────────────────────────────
// Internal router only, admin-gated — same defense-in-depth pattern as
// every other admin surface above. Read-only: this is compliance/ops
// visibility into what the screening hooks found, never a write path.

// ─── Regulatory reporting (docs/plan/20 Task T2) ────────────────────────────
// Internal router only, admin-gated. Purely read-only over the
// migrations/000018 views — zero write path. format=csv streams row by row
// (encoding/csv straight to the response), same pattern as
// writeStatementCSV (docs/plan/15 Task T2) — never fully buffered.

// maxReportDays caps a single report query's date range — same rationale as
// maxStatementDays: bound the view scan and the CSV response size on a
// small box, not a hard business constraint.
const maxReportDays = 366

func (h *handler) getReport(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}

	kind := r.PathValue("kind")
	if kind != "position" && kind != "mutation" && kind != "recon" {
		response.BadRequest(w, "kind must be 'position', 'mutation', or 'recon'")
		return
	}

	fromRaw := r.URL.Query().Get("from")
	toRaw := r.URL.Query().Get("to")
	if fromRaw == "" || toRaw == "" {
		response.BadRequest(w, "from and to are required (YYYY-MM-DD)")
		return
	}
	from, err := time.Parse("2006-01-02", fromRaw)
	if err != nil {
		response.BadRequest(w, "from must be YYYY-MM-DD")
		return
	}
	to, err := time.Parse("2006-01-02", toRaw)
	if err != nil {
		response.BadRequest(w, "to must be YYYY-MM-DD")
		return
	}
	if from.After(to) {
		response.BadRequest(w, "from must not be after to")
		return
	}
	if to.Sub(from) > maxReportDays*24*time.Hour {
		response.BadRequest(w, fmt.Sprintf("range too large, narrow the period (max %d days)", maxReportDays))
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		response.BadRequest(w, "format must be 'json' or 'csv'")
		return
	}

	switch kind {
	case "position":
		rows, err := h.svc.GetDailyPositionReport(r.Context(), from, to)
		if err != nil {
			writeError(w, err)
			return
		}
		if format == "csv" {
			writeDailyPositionCSV(w, rows)
			return
		}
		out := make([]dailyPositionResponse, len(rows))
		for i, row := range rows {
			out[i] = toDailyPositionResponse(row)
		}
		response.OK(w, listDailyPositionResponse{Rows: out})

	case "mutation":
		rows, err := h.svc.GetDailyMutationReport(r.Context(), from, to)
		if err != nil {
			writeError(w, err)
			return
		}
		if format == "csv" {
			writeDailyMutationCSV(w, rows)
			return
		}
		out := make([]dailyMutationResponse, len(rows))
		for i, row := range rows {
			out[i] = toDailyMutationResponse(row)
		}
		response.OK(w, listDailyMutationResponse{Rows: out})

	case "recon":
		rows, err := h.svc.GetReconSummaryReport(r.Context(), from, to)
		if err != nil {
			writeError(w, err)
			return
		}
		if format == "csv" {
			writeReconSummaryCSV(w, rows)
			return
		}
		out := make([]reconSummaryResponse, len(rows))
		for i, row := range rows {
			out[i] = toReconSummaryResponse(row)
		}
		response.OK(w, listReconSummaryResponse{Rows: out})
	}
}

func writeDailyPositionCSV(w http.ResponseWriter, rows []model.ReportDailyPosition) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=report_position.csv")
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"as_of_date", "currency", "account_type", "owner_type", "account_count", "total_balance"})
	for _, row := range rows {
		_ = cw.Write([]string{
			row.AsOfDate.Format("2006-01-02"), row.Currency, row.AccountType, row.OwnerType,
			strconv.Itoa(row.AccountCount), row.TotalBalance.String(),
		})
	}
	cw.Flush()
}

func writeDailyMutationCSV(w http.ResponseWriter, rows []model.ReportDailyMutation) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=report_mutation.csv")
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"report_date", "tx_type", "currency", "tx_count", "total_amount"})
	for _, row := range rows {
		_ = cw.Write([]string{
			row.ReportDate.Format("2006-01-02"), row.TxType, row.Currency,
			strconv.Itoa(row.TxCount), row.TotalAmount.String(),
		})
	}
	cw.Flush()
}

func writeReconSummaryCSV(w http.ResponseWriter, rows []model.ReportReconSummary) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=report_recon.csv")
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"batch_id", "gateway", "report_date", "source_filename", "batch_status",
		"declared_row_count", "item_count", "matched_count", "missing_internal_count",
		"missing_external_count", "amount_mismatch_count", "resolved_count"})
	for _, row := range rows {
		_ = cw.Write([]string{
			row.BatchID.String(), row.Gateway, row.ReportDate.Format("2006-01-02"), row.SourceFilename, row.BatchStatus,
			strconv.Itoa(row.DeclaredRowCount), strconv.Itoa(row.ItemCount), strconv.Itoa(row.MatchedCount),
			strconv.Itoa(row.MissingInternalCount), strconv.Itoa(row.MissingExternalCount),
			strconv.Itoa(row.AmountMismatchCount), strconv.Itoa(row.ResolvedCount),
		})
	}
	cw.Flush()
}
