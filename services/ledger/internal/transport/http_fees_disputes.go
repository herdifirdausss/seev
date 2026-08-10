package transport

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/platform/money/currency"
	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/ledger/internal/feepolicy"
	constant "github.com/herdifirdausss/seev/services/ledger/internal/ledger/constants"
	apperror "github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

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
	rule := feepolicy.Rule{
		TxType: req.TxType, Gateway: req.Gateway, Currency: req.Currency, UserID: userID,
		FlatMinorUnits: req.FlatMinorUnits, PercentBasisPts: req.PercentBasisPts,
		FeeGateway: feeGateway, Enabled: enabled,
	}
	if h.feeApproval != nil {
		actor, actorOK := currentUserID(r)
		if !actorOK {
			response.Unauthorized(w, "invalid or missing admin identity")
			return
		}
		version, createErr := h.feeApproval.CreateDraft(r.Context(), rule, actor.String(), req.Reason)
		if createErr != nil {
			writeError(w, createErr)
			return
		}
		response.Accepted(w, feeRuleVersionResponse(version))
		return
	}
	created, err := h.feePolicy.Create(r.Context(), rule)
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, toFeeRuleResponse(created))
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
	rule := feepolicy.Rule{
		ID: id, TxType: req.TxType, Gateway: req.Gateway, Currency: req.Currency, UserID: userID,
		FlatMinorUnits: req.FlatMinorUnits, PercentBasisPts: req.PercentBasisPts,
		FeeGateway: feeGateway, Enabled: enabled,
	}
	if h.feeApproval != nil {
		actor, actorOK := currentUserID(r)
		if !actorOK {
			response.Unauthorized(w, "invalid or missing admin identity")
			return
		}
		version, createErr := h.feeApproval.CreateDraft(r.Context(), rule, actor.String(), req.Reason)
		if createErr != nil {
			writeError(w, createErr)
			return
		}
		response.Accepted(w, feeRuleVersionResponse(version))
		return
	}
	updated, err := h.feePolicy.Update(r.Context(), rule)
	if errors.Is(err, feepolicy.ErrRuleNotFound) {
		response.NotFound(w, "fee rule not found")
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toFeeRuleResponse(updated))
}

func feeRuleVersionResponse(version model.FeeRuleVersion) map[string]any {
	return map[string]any{
		"id": version.ID, "rule_id": version.RuleID, "version": version.Version,
		"tx_type": version.TxType, "gateway": version.Gateway, "currency": version.Currency,
		"user_id": version.UserID, "flat_minor_units": version.FlatMinorUnits,
		"percent_basis_pts": version.PercentBasisPts, "fee_gateway": version.FeeGateway,
		"enabled": version.Enabled, "status": version.Status, "created_by": version.CreatedBy,
		"submitted_by": version.SubmittedBy, "approved_by": version.ApprovedBy,
		"rejected_by": version.RejectedBy, "decision_reason": version.DecisionReason,
		"effective_from": version.EffectiveFrom, "effective_until": version.EffectiveUntil,
		"created_at": version.CreatedAt, "updated_at": version.UpdatedAt,
	}
}

func (h *handler) listFeeRuleVersions(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) || h.feeApproval == nil {
		response.Forbidden(w, "fee-rule approval is not configured")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid fee rule id")
		return
	}
	items, err := h.feeApproval.ListVersions(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, feeRuleVersionResponse(item))
	}
	response.OK(w, map[string]any{"versions": result})
}

func (h *handler) feeRuleAction(w http.ResponseWriter, r *http.Request, action string) {
	if !isAdmin(r) || h.feeApproval == nil {
		response.Forbidden(w, "fee-rule approval is not configured")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid fee-rule version id")
		return
	}
	actor, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing admin identity")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 && !response.Decode(w, r, &body) {
		return
	}
	var version model.FeeRuleVersion
	switch action {
	case "submit":
		version, err = h.feeApproval.Submit(r.Context(), id, actor.String())
	case "approve":
		if !isAdminChecker(r) {
			response.Forbidden(w, "checker privileges required")
			return
		}
		version, err = h.feeApproval.Approve(r.Context(), id, actor.String(), body.Reason)
	case "reject":
		if !isAdminChecker(r) {
			response.Forbidden(w, "checker privileges required")
			return
		}
		version, err = h.feeApproval.Reject(r.Context(), id, actor.String(), body.Reason)
	default:
		response.BadRequest(w, "unknown fee-rule action")
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, feeRuleVersionResponse(version))
}

func (h *handler) submitFeeRule(w http.ResponseWriter, r *http.Request) {
	h.feeRuleAction(w, r, "submit")
}

func (h *handler) approveFeeRule(w http.ResponseWriter, r *http.Request) {
	h.feeRuleAction(w, r, "approve")
}

func (h *handler) rejectFeeRule(w http.ResponseWriter, r *http.Request) {
	h.feeRuleAction(w, r, "reject")
}

type chargebackDisputeAPI interface {
	OpenChargebackDispute(context.Context, uuid.UUID, string, string, string, decimal.Decimal, string, *time.Time, string) (uuid.UUID, error)
	GetChargebackDispute(context.Context, uuid.UUID) (model.ChargebackDispute, error)
	ListOpenChargebackDisputes(context.Context, int, int) ([]model.ChargebackDispute, error)
	SubmitChargebackDisputeEvidence(context.Context, uuid.UUID, string, string) error
	ResolveChargebackDispute(context.Context, uuid.UUID, string, string, string) error
	ListChargebackDisputeStatusChanges(context.Context, uuid.UUID) ([]model.ChargebackDisputeStatusChange, error)
}

func (h *handler) disputes(w http.ResponseWriter) (chargebackDisputeAPI, bool) {
	api, ok := h.svc.(chargebackDisputeAPI)
	if !ok {
		response.ServiceUnavailable(w, "DISPUTES_UNAVAILABLE", "dispute case management is not configured")
		return nil, false
	}
	return api, true
}

func disputeResponse(d model.ChargebackDispute) map[string]any {
	return map[string]any{
		"id": d.ID, "original_tx_id": d.OriginalTxID, "chargeback_tx_id": d.ChargebackTxID,
		"dispute_ref": d.DisputeRef, "card_network": d.CardNetwork, "reason_code": d.ReasonCode,
		"amount": d.Amount.String(), "currency": d.Currency, "status": d.Status,
		"evidence_due_at": d.EvidenceDueAt, "evidence_ref": d.EvidenceRef,
		"resolved_at": d.ResolvedAt, "resolved_by": d.ResolvedBy,
		"resolution_reason": d.ResolutionReason, "created_by": d.CreatedBy,
		"created_at": d.CreatedAt, "updated_at": d.UpdatedAt,
	}
}

func (h *handler) openDispute(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	api, ok := h.disputes(w)
	if !ok {
		return
	}
	actor, actorOK := currentUserID(r)
	if !actorOK {
		response.Unauthorized(w, "invalid or missing admin identity")
		return
	}
	var req openDisputeRequest
	if !response.Decode(w, r, &req) {
		return
	}
	originalID, err := uuid.Parse(req.OriginalTxID)
	if err != nil {
		response.BadRequest(w, "original_tx_id must be a valid UUID")
		return
	}
	amount, err := decimalFromString(req.Amount)
	if err != nil {
		response.BadRequest(w, "amount must be an integer minor-unit decimal string")
		return
	}
	var dueAt *time.Time
	if req.EvidenceDueAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339, req.EvidenceDueAt)
		if parseErr != nil {
			response.BadRequest(w, "evidence_due_at must be RFC3339")
			return
		}
		dueAt = &parsed
	}
	id, err := api.OpenChargebackDispute(r.Context(), originalID, req.DisputeRef, req.CardNetwork, req.ReasonCode, amount, req.Currency, dueAt, actor.String())
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, map[string]any{"id": id})
}

func (h *handler) listDisputes(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	api, ok := h.disputes(w)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, err := api.ListOpenChargebackDisputes(r.Context(), limit, offset)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, disputeResponse(item))
	}
	response.OK(w, map[string]any{"disputes": out})
}

func (h *handler) getDispute(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	api, ok := h.disputes(w)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid dispute id")
		return
	}
	d, err := api.GetChargebackDispute(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, disputeResponse(d))
}

func (h *handler) listDisputeStatusChanges(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	api, ok := h.disputes(w)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid dispute id")
		return
	}
	items, err := api.ListChargebackDisputeStatusChanges(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, map[string]any{"status_changes": items})
}

func (h *handler) submitDisputeEvidence(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	api, ok := h.disputes(w)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid dispute id")
		return
	}
	actor, actorOK := currentUserID(r)
	if !actorOK {
		response.Unauthorized(w, "invalid or missing admin identity")
		return
	}
	var req disputeEvidenceRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if err := api.SubmitChargebackDisputeEvidence(r.Context(), id, req.EvidenceRef, actor.String()); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, map[string]any{"status": "evidence_submitted"})
}

func (h *handler) resolveDispute(w http.ResponseWriter, r *http.Request) {
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	api, ok := h.disputes(w)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid dispute id")
		return
	}
	actor, actorOK := currentUserID(r)
	if !actorOK {
		response.Unauthorized(w, "invalid or missing admin identity")
		return
	}
	var req disputeResolveRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if err := api.ResolveChargebackDispute(r.Context(), id, req.Status, actor.String(), req.Reason); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, map[string]any{"status": req.Status})
}

// createQuote serves POST /fees/quote (docs/roadmap/archive/38 Task T3, public router
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
	if quoteCurrency != "" {
		if err := currency.ValidateCode(quoteCurrency); err != nil {
			response.ErrorStatus(w, http.StatusBadRequest, apperror.ErrCurrencyInvalid.Error(), err.Error())
			return
		}
		if !currency.IsValid(quoteCurrency) {
			response.ErrorStatus(w, http.StatusBadRequest, apperror.ErrCurrencyInvalid.Error(), "currency is not registered")
			return
		}
	}
	if quoteCurrency == "" {
		resolved, cerr := h.svc.GetUserCurrency(r.Context(), userID, "")
		if cerr == nil {
			quoteCurrency = resolved
		}
	}
	if err := currency.ValidateCode(quoteCurrency); err != nil || !currency.IsValid(quoteCurrency) {
		response.ErrorStatus(w, http.StatusBadRequest, apperror.ErrCurrencyInvalid.Error(), "currency is not registered")
		return
	}
	if validator, ok := h.svc.(interface {
		ValidateCurrency(context.Context, string, string) error
	}); ok {
		if operation := feeQuoteCurrencyOperation(req.TransactionType); operation != "" {
			if err := validator.ValidateCurrency(r.Context(), quoteCurrency, operation); err != nil {
				writeError(w, err)
				return
			}
		}
	}

	q, err := h.feePolicy.CreateQuote(r.Context(), userID, req.TransactionType, req.Gateway, quoteCurrency, amount, h.feeQuoteTTL)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.Created(w, toQuoteResponse(q))
}

func feeQuoteCurrencyOperation(transactionType string) string {
	switch transactionType {
	case "money_in":
		return "topup"
	case "withdraw_initiate", "withdraw_pending", "withdraw_pending_settle", "withdraw_pending_cancel", "withdraw_settle", "withdraw_cancel", "money_out":
		return "payout"
	case "transfer_p2p", "transfer_pocket", "escrow_hold", "escrow_release", "escrow_refund", "refund":
		return "transfer"
	default:
		return ""
	}
}
