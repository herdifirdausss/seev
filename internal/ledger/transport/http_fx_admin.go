package transport

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

type fxAdminService interface {
	ListCurrencies(context.Context, uuid.UUID) ([]model.CurrencyInfo, error)
	ListFXPairs(context.Context) ([]model.FXPair, error)
	ListFXPositions(context.Context) ([]model.FXPosition, error)
	GetFXDailyPositionReport(context.Context, time.Time, time.Time) ([]model.FXDailyPosition, error)
	ReconcileFXConversions(context.Context, time.Time, time.Time, int) (model.FXReconciliationReport, error)
	CreateFXRebalance(context.Context, string, uuid.UUID, string, int64, bool, string, string) (uuid.UUID, error)
	ListFXRateVersions(context.Context, uuid.UUID, uuid.UUID) ([]model.FXRateVersion, error)
	GetFXQuoteForAdmin(context.Context, uuid.UUID) (model.FXQuote, error)
	GetFXConversionForAdmin(context.Context, uuid.UUID) (model.FXConversion, error)
	UpdateFXCurrencyPolicy(context.Context, string, string, map[string]bool, string) error
	UpdateFXPairStatus(context.Context, uuid.UUID, string, string) error
	UpdateFXPairControls(context.Context, uuid.UUID, bool, bool, string) error
	UpdateFXDirectionControls(context.Context, uuid.UUID, bool, bool, bool, int64, int64, int64, string) error
	UpdateFXPositionLimit(context.Context, uuid.UUID, string, int64, int64, string) error
	CreateFXRate(context.Context, uuid.UUID, uuid.UUID, string, time.Time, *time.Time, string) (model.FXRateVersion, error)
	SubmitFXRate(context.Context, uuid.UUID, string) (model.FXRateVersion, error)
	ApproveFXRate(context.Context, uuid.UUID, string) (model.FXRateVersion, error)
	RejectFXRate(context.Context, uuid.UUID, string, string) (model.FXRateVersion, error)
	RetireFXRate(context.Context, uuid.UUID, string) (model.FXRateVersion, error)
}

type fxPairControlsHTTPRequest struct {
	NewQuotesPaused   bool `json:"new_quotes_paused"`
	ConversionsPaused bool `json:"conversions_paused"`
}

type updateFXCurrencyPolicyHTTPRequest struct {
	Status     string          `json:"status,omitempty"`
	Operations map[string]bool `json:"operations,omitempty"`
}

type fxPairStatusHTTPRequest struct {
	Status string `json:"status"`
}

type fxDirectionControlsHTTPRequest struct {
	Enabled           bool   `json:"enabled"`
	NewQuotesPaused   bool   `json:"new_quotes_paused"`
	ConversionsPaused bool   `json:"conversions_paused"`
	MinSourceAmount   string `json:"min_source_amount"`
	MaxSourceAmount   string `json:"max_source_amount"`
	SpreadBasisPoints int64  `json:"spread_basis_points"`
}

type fxPositionLimitHTTPRequest struct {
	PairID         string `json:"pair_id"`
	Currency       string `json:"currency"`
	MinimumBalance string `json:"minimum_balance"`
	MaximumBalance string `json:"maximum_balance"`
}

type createFXRebalanceHTTPRequest struct {
	PairID    string `json:"pair_id"`
	Currency  string `json:"currency"`
	Amount    string `json:"amount"`
	Increase  bool   `json:"increase"`
	Reason    string `json:"reason"`
	TicketRef string `json:"ticket_ref,omitempty"`
}

type createFXRateHTTPRequest struct {
	PairID        string `json:"pair_id"`
	DirectionID   string `json:"direction_id"`
	ReferenceRate string `json:"reference_rate"`
	EffectiveFrom string `json:"effective_from,omitempty"`
	EffectiveTo   string `json:"effective_to,omitempty"`
}

type rejectFXRateHTTPRequest struct {
	Reason string `json:"reason,omitempty"`
}

func (h *handler) fxAdmin() (fxAdminService, bool) {
	svc, ok := h.svc.(fxAdminService)
	return svc, ok
}

func (h *handler) requireFXAdmin(w http.ResponseWriter, r *http.Request) (fxAdminService, string, bool) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return nil, "", false
	}
	svc, ok := h.fxAdmin()
	if !ok {
		response.InternalServerError(w, errors.New("FX admin service is unavailable"))
		return nil, "", false
	}
	actor := middleware.UserIDFromCtx(r.Context())
	if actor == "" {
		response.Unauthorized(w, "operator identity is unavailable")
		return nil, "", false
	}
	return svc, actor, true
}

func (h *handler) adminListFXPairs(w http.ResponseWriter, r *http.Request) {
	svc, _, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	pairs, err := svc.ListFXPairs(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeFXPairs(w, pairs)
}

func (h *handler) adminListFXCurrencies(w http.ResponseWriter, r *http.Request) {
	svc, _, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	items, err := svc.ListCurrencies(r.Context(), uuid.Nil)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]currencyHTTPResponse, 0, len(items))
	for _, item := range items {
		out = append(out, currencyHTTPResponse{
			Code: item.Code, MinorUnit: item.MinorUnit, Status: item.Status,
			Operations: item.Operations, UserEnabled: item.UserEnabled,
		})
	}
	response.OK(w, listCurrenciesHTTPResponse{Currencies: out})
}

func (h *handler) adminUpdateFXCurrencyPolicy(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	var req updateFXCurrencyPolicyHTTPRequest
	if !response.Decode(w, r, &req) {
		return
	}
	code := r.PathValue("currency")
	if err := svc.UpdateFXCurrencyPolicy(r.Context(), code, req.Status, req.Operations, actor); err != nil {
		writeError(w, err)
		return
	}
	items, err := svc.ListCurrencies(r.Context(), uuid.Nil)
	if err != nil {
		writeError(w, err)
		return
	}
	for _, item := range items {
		if item.Code == code {
			response.OK(w, currencyHTTPResponse{
				Code: item.Code, MinorUnit: item.MinorUnit, Status: item.Status,
				Operations: item.Operations, UserEnabled: item.UserEnabled,
			})
			return
		}
	}
	response.OK(w, map[string]any{"currency": code, "status": req.Status, "operations": req.Operations})
}

func (h *handler) writeFXPairs(w http.ResponseWriter, pairs []model.FXPair) {
	// Reuse the public shape so the Admin BFF and the user-facing pair page
	// cannot drift on direction, spread, or rate-convention fields.
	out := make([]fxPairHTTPResponse, 0, len(pairs))
	for _, pair := range pairs {
		directions := make([]fxDirectionHTTPResponse, 0, len(pair.Directions))
		for _, direction := range pair.Directions {
			directions = append(directions, fxDirectionHTTPResponse{
				ID: direction.ID, SourceCurrency: direction.SourceCurrency,
				TargetCurrency: direction.TargetCurrency, Enabled: direction.Enabled,
				NewQuotesPaused: direction.NewQuotesPaused, ConversionsPaused: direction.ConversionsPaused,
				MinSourceAmount: strconv.FormatInt(direction.MinSourceAmount, 10),
				MaxSourceAmount: strconv.FormatInt(direction.MaxSourceAmount, 10),
				SpreadBasisPoints: direction.SpreadBasisPoints,
			})
		}
		out = append(out, fxPairHTTPResponse{
			ID: pair.ID, PairCode: pair.PairCode, BaseCurrency: pair.BaseCurrency,
			QuoteCurrency: pair.QuoteCurrency, RateConvention: pair.RateConvention,
			Status: pair.Status, RateSource: pair.RateSource, QuoteTTLSeconds: pair.QuoteTTLSeconds,
			RoundingMode: pair.RoundingMode, PairPolicyVersion: pair.PairPolicyVersion,
			Directions: directions,
		})
	}
	response.OK(w, map[string]any{"pairs": out})
}

func (h *handler) adminListFXPositions(w http.ResponseWriter, r *http.Request) {
	svc, _, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	positions, err := svc.ListFXPositions(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(positions))
	for _, position := range positions {
		out = append(out, map[string]any{
			"pair_id": position.PairID, "pair_code": position.PairCode,
			"currency": position.Currency, "account_id": position.AccountID,
			"minor_unit": position.MinorUnit, "balance": strconv.FormatInt(position.Balance, 10),
			"minimum_balance": strconv.FormatInt(position.MinimumBalance, 10),
			"maximum_balance": strconv.FormatInt(position.MaximumBalance, 10),
			"warning_minimum_balance": strconv.FormatInt(position.WarningMinimumBalance, 10),
			"warning_maximum_balance": strconv.FormatInt(position.WarningMaximumBalance, 10),
			"critical_minimum_balance": strconv.FormatInt(position.CriticalMinimumBalance, 10),
			"critical_maximum_balance": strconv.FormatInt(position.CriticalMaximumBalance, 10),
			"state": position.State,
		})
		if position.LastConversionAt != nil {
			out[len(out)-1]["last_conversion_at"] = position.LastConversionAt.UTC().Format(time.RFC3339Nano)
		}
	}
	response.OK(w, map[string]any{"positions": out})
}

func (h *handler) adminGetFXDailyPositionReport(w http.ResponseWriter, r *http.Request) {
	svc, _, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	from, to, err := parseFXReconciliationWindow(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}
	rows, err := svc.GetFXDailyPositionReport(r.Context(), from, to)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"date": row.Date.UTC().Format("2006-01-02"),
			"pair_id": row.PairID, "pair_code": row.PairCode, "currency": row.Currency,
			"account_id": row.AccountID, "minor_unit": row.MinorUnit,
			"opening_balance": strconv.FormatInt(row.OpeningBalance, 10),
			"conversion_inflow": strconv.FormatInt(row.ConversionInflow, 10),
			"conversion_outflow": strconv.FormatInt(row.ConversionOutflow, 10),
			"rebalance_credit": strconv.FormatInt(row.RebalanceCredit, 10),
			"rebalance_debit": strconv.FormatInt(row.RebalanceDebit, 10),
			"closing_balance": strconv.FormatInt(row.ClosingBalance, 10),
			"state": row.State,
		})
	}
	response.OK(w, map[string]any{"rows": out})
}

func (h *handler) adminReconcileFXConversions(w http.ResponseWriter, r *http.Request) {
	svc, _, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	from, to, err := parseFXReconciliationWindow(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > 1000 {
			response.BadRequest(w, "limit must be an integer between 1 and 1000")
			return
		}
	}
	report, err := svc.ReconcileFXConversions(r.Context(), from, to, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(report.Items))
	for _, item := range report.Items {
		items = append(items, map[string]any{
			"resource_type": item.ResourceType, "resource_id": item.ResourceID,
			"conversion_id": item.ConversionID, "quote_id": item.QuoteID,
			"source_currency": item.SourceCurrency, "target_currency": item.TargetCurrency,
			"source_amount": strconv.FormatInt(item.SourceAmount, 10),
			"target_amount": strconv.FormatInt(item.TargetAmount, 10),
			"source_transaction_id": item.SourceTransactionID,
			"target_transaction_id": item.TargetTransactionID,
			"source_leg_status": item.SourceLegStatus, "target_leg_status": item.TargetLegStatus,
			"source_link_valid": item.SourceLinkValid, "target_link_valid": item.TargetLinkValid,
			"source_leg_balanced": item.SourceLegBalanced, "target_leg_balanced": item.TargetLegBalanced,
			"quote_valid": item.QuoteValid, "position_accounts_valid": item.PositionAccountsValid,
			"position_balances_valid": item.PositionBalancesValid, "aggregate_event_present": item.AggregateEventPresent,
			"status": item.Status, "reason": item.Reason,
			"checked_at": item.CheckedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	response.OK(w, map[string]any{
		"from": report.From.UTC().Format(time.RFC3339Nano),
		"to": report.To.UTC().Format(time.RFC3339Nano),
		"total": report.Total, "reconciled": report.Reconciled, "critical": report.Critical,
		"items": items,
	})
}

func (h *handler) adminCreateFXRebalance(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	var req createFXRebalanceHTTPRequest
	if !response.Decode(w, r, &req) {
		return
	}
	pairID, err := uuid.Parse(req.PairID)
	if err != nil {
		response.BadRequest(w, "pair_id must be a valid UUID")
		return
	}
	amount, err := parseSignedMinor(req.Amount)
	if err != nil || amount <= 0 {
		response.BadRequest(w, "amount must be a positive integer string")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		response.BadRequest(w, "reason is required")
		return
	}
	adjustmentID, err := svc.CreateFXRebalance(r.Context(), actor, pairID, req.Currency, amount, req.Increase, strings.TrimSpace(req.Reason), strings.TrimSpace(req.TicketRef))
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, map[string]any{"adjustment_id": adjustmentID, "status": "pending"})
}

func (h *handler) adminListFXRates(w http.ResponseWriter, r *http.Request) {
	svc, _, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	pairID, err := parseOptionalUUID(r.URL.Query().Get("pair_id"))
	if err != nil || pairID == uuid.Nil {
		response.BadRequest(w, "pair_id must be a valid UUID")
		return
	}
	directionID, err := parseOptionalUUID(r.URL.Query().Get("direction_id"))
	if err != nil {
		response.BadRequest(w, "direction_id must be a valid UUID")
		return
	}
	rates, err := svc.ListFXRateVersions(r.Context(), pairID, directionID)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rates))
	for _, rate := range rates {
		out = append(out, fxRateHTTPResponse(rate))
	}
	response.OK(w, map[string]any{"rates": out})
}

func (h *handler) adminCreateFXRate(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	var req createFXRateHTTPRequest
	if !response.Decode(w, r, &req) {
		return
	}
	pairID, err := uuid.Parse(req.PairID)
	if err != nil {
		response.BadRequest(w, "pair_id must be a valid UUID")
		return
	}
	directionID, err := uuid.Parse(req.DirectionID)
	if err != nil {
		response.BadRequest(w, "direction_id must be a valid UUID")
		return
	}
	effectiveFrom, effectiveTo, err := parseFXRateWindow(req.EffectiveFrom, req.EffectiveTo)
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}
	rate, err := svc.CreateFXRate(r.Context(), pairID, directionID, req.ReferenceRate, effectiveFrom, effectiveTo, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, fxRateHTTPResponse(rate))
}

func (h *handler) adminSubmitFXRate(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	rateID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "id must be a valid UUID")
		return
	}
	rate, err := svc.SubmitFXRate(r.Context(), rateID, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, fxRateHTTPResponse(rate))
}

func (h *handler) adminApproveFXRate(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	rateID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "id must be a valid UUID")
		return
	}
	rate, err := svc.ApproveFXRate(r.Context(), rateID, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, fxRateHTTPResponse(rate))
}

func (h *handler) adminRejectFXRate(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	rateID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "id must be a valid UUID")
		return
	}
	var req rejectFXRateHTTPRequest
	if !response.Decode(w, r, &req) {
		return
	}
	rate, err := svc.RejectFXRate(r.Context(), rateID, actor, strings.TrimSpace(req.Reason))
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, fxRateHTTPResponse(rate))
}

func (h *handler) adminRetireFXRate(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	rateID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "id must be a valid UUID")
		return
	}
	rate, err := svc.RetireFXRate(r.Context(), rateID, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, fxRateHTTPResponse(rate))
}

func (h *handler) adminUpdateFXPairControls(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	var req fxPairControlsHTTPRequest
	if !response.Decode(w, r, &req) {
		return
	}
	pairID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "id must be a valid UUID")
		return
	}
	if err := svc.UpdateFXPairControls(r.Context(), pairID, req.NewQuotesPaused, req.ConversionsPaused, actor); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, map[string]any{"pair_id": pairID, "new_quotes_paused": req.NewQuotesPaused, "conversions_paused": req.ConversionsPaused})
}

func (h *handler) adminUpdateFXPairStatus(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	var req fxPairStatusHTTPRequest
	if !response.Decode(w, r, &req) {
		return
	}
	pairID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "id must be a valid UUID")
		return
	}
	if err := svc.UpdateFXPairStatus(r.Context(), pairID, req.Status, actor); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, map[string]any{"pair_id": pairID, "status": req.Status})
}

func (h *handler) adminUpdateFXDirectionControls(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	var req fxDirectionControlsHTTPRequest
	if !response.Decode(w, r, &req) {
		return
	}
	directionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "id must be a valid UUID")
		return
	}
	minimum, err := parseSignedMinor(req.MinSourceAmount)
	if err != nil {
		response.BadRequest(w, "min_source_amount must be a signed integer string")
		return
	}
	maximum, err := parseSignedMinor(req.MaxSourceAmount)
	if err != nil {
		response.BadRequest(w, "max_source_amount must be a signed integer string")
		return
	}
	if err := svc.UpdateFXDirectionControls(r.Context(), directionID, req.Enabled, req.NewQuotesPaused, req.ConversionsPaused, minimum, maximum, req.SpreadBasisPoints, actor); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, map[string]any{
		"direction_id": directionID, "enabled": req.Enabled,
		"new_quotes_paused": req.NewQuotesPaused, "conversions_paused": req.ConversionsPaused,
		"min_source_amount": req.MinSourceAmount, "max_source_amount": req.MaxSourceAmount,
		"spread_basis_points": req.SpreadBasisPoints,
	})
}

func (h *handler) adminUpdateFXPositionLimit(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	var req fxPositionLimitHTTPRequest
	if !response.Decode(w, r, &req) {
		return
	}
	pairID, err := uuid.Parse(req.PairID)
	if err != nil {
		response.BadRequest(w, "pair_id must be a valid UUID")
		return
	}
	minimum, err := parseSignedMinor(req.MinimumBalance)
	if err != nil {
		response.BadRequest(w, "minimum_balance must be a signed integer string")
		return
	}
	maximum, err := parseSignedMinor(req.MaximumBalance)
	if err != nil {
		response.BadRequest(w, "maximum_balance must be a signed integer string")
		return
	}
	if err := svc.UpdateFXPositionLimit(r.Context(), pairID, req.Currency, minimum, maximum, actor); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, map[string]any{"pair_id": pairID, "currency": req.Currency, "minimum_balance": req.MinimumBalance, "maximum_balance": req.MaximumBalance})
}

func (h *handler) adminGetFXQuote(w http.ResponseWriter, r *http.Request) {
	svc, _, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	quoteID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "id must be a valid UUID")
		return
	}
	quote, err := svc.GetFXQuoteForAdmin(r.Context(), quoteID)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toFXQuoteHTTPResponse(quote))
}

func (h *handler) adminGetFXConversion(w http.ResponseWriter, r *http.Request) {
	svc, _, ok := h.requireFXAdmin(w, r)
	if !ok {
		return
	}
	conversionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "id must be a valid UUID")
		return
	}
	conversion, err := svc.GetFXConversionForAdmin(r.Context(), conversionID)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toFXConversionHTTPResponse(conversion))
}

func fxRateHTTPResponse(rate model.FXRateVersion) map[string]any {
	result := map[string]any{
		"id": rate.ID, "pair_id": rate.PairID, "direction_id": rate.DirectionID,
		"version": rate.Version, "reference_rate": rate.ReferenceRate,
		"rate_source": rate.RateSource, "status": rate.Status,
		"effective_from": rate.EffectiveFrom.UTC().Format(time.RFC3339Nano),
		"created_by": rate.CreatedBy, "submitted_by": rate.SubmittedBy,
		"approved_by": rate.ApprovedBy, "created_at": rate.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if rate.EffectiveTo != nil {
		result["effective_to"] = rate.EffectiveTo.UTC().Format(time.RFC3339Nano)
	}
	if rate.SubmittedAt != nil {
		result["submitted_at"] = rate.SubmittedAt.UTC().Format(time.RFC3339Nano)
	}
	if rate.ApprovedAt != nil {
		result["approved_at"] = rate.ApprovedAt.UTC().Format(time.RFC3339Nano)
	}
	if rate.RetiredAt != nil {
		result["retired_at"] = rate.RetiredAt.UTC().Format(time.RFC3339Nano)
	}
	return result
}

func parseOptionalUUID(raw string) (uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(raw)
}

func parseFXRateWindow(fromRaw, toRaw string) (time.Time, *time.Time, error) {
	var from time.Time
	var err error
	if strings.TrimSpace(fromRaw) != "" {
		from, err = time.Parse(time.RFC3339Nano, fromRaw)
		if err != nil {
			return time.Time{}, nil, errors.New("effective_from must be RFC3339")
		}
	}
	var to *time.Time
	if strings.TrimSpace(toRaw) != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, toRaw)
		if parseErr != nil {
			return time.Time{}, nil, errors.New("effective_to must be RFC3339")
		}
		to = &parsed
	}
	return from, to, nil
}

func parseFXReconciliationWindow(fromRaw, toRaw string) (time.Time, time.Time, error) {
	var from, to time.Time
	var err error
	if strings.TrimSpace(fromRaw) != "" {
		from, err = time.Parse(time.RFC3339Nano, fromRaw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("from must be RFC3339")
		}
	}
	if strings.TrimSpace(toRaw) != "" {
		to, err = time.Parse(time.RFC3339Nano, toRaw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("to must be RFC3339")
		}
	}
	return from, to, nil
}

func parseSignedMinor(raw string) (int64, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return 0, errors.New("invalid signed integer")
	}
	if raw == "-" || raw == "+" {
		return 0, errors.New("invalid signed integer")
	}
	start := 0
	if raw[0] == '-' || raw[0] == '+' {
		start = 1
	}
	for _, r := range raw[start:] {
		if r < '0' || r > '9' {
			return 0, errors.New("invalid signed integer")
		}
	}
	return strconv.ParseInt(raw, 10, 64)
}
