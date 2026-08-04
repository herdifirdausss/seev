package transport

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

type multiCurrencyService interface {
	ListCurrencies(ctx context.Context, userID uuid.UUID) ([]model.CurrencyInfo, error)
	ListCurrencyBalances(ctx context.Context, userID uuid.UUID) ([]model.CurrencyBalance, error)
	GetCurrencyBalance(ctx context.Context, userID uuid.UUID, code string) (model.CurrencyBalance, error)
	EnableUserCurrency(ctx context.Context, userID uuid.UUID, code string) ([]model.Account, error)
	ListFXPairs(ctx context.Context) ([]model.FXPair, error)
	CreateFXQuote(ctx context.Context, userID uuid.UUID, sourceCode, targetCode string, sourceAmount int64, requestKey string) (model.FXQuote, error)
	GetFXQuote(ctx context.Context, userID, quoteID uuid.UUID) (model.FXQuote, error)
	ExecuteFXConversion(ctx context.Context, userID, quoteID uuid.UUID, idempotencyKey string, expectedSource, expectedTarget int64) (model.FXConversion, error)
	GetFXConversion(ctx context.Context, userID, conversionID uuid.UUID) (model.FXConversion, error)
}

type currencyHTTPResponse struct {
	Code        string          `json:"code"`
	MinorUnit   int16           `json:"minor_unit"`
	Status      string          `json:"status"`
	Operations  map[string]bool `json:"operations"`
	UserEnabled bool            `json:"user_enabled"`
}

type listCurrenciesHTTPResponse struct {
	Currencies []currencyHTTPResponse `json:"currencies"`
}

type currencyBalanceHTTPResponse struct {
	Currency    string          `json:"currency"`
	MinorUnit   int16           `json:"minor_unit"`
	Status      string          `json:"status"`
	Operations  map[string]bool `json:"operations"`
	UserEnabled bool            `json:"user_enabled"`
	Available   string          `json:"available"`
	Hold        string          `json:"hold"`
	Pending     string          `json:"pending"`
	Frozen      string          `json:"frozen"`
}

type listCurrencyBalancesHTTPResponse struct {
	Balances []currencyBalanceHTTPResponse `json:"balances"`
}

type enableCurrencyHTTPResponse struct {
	Currency string            `json:"currency"`
	Enabled  bool              `json:"enabled"`
	Accounts []accountResponse `json:"accounts"`
}

type createFXQuoteHTTPRequest struct {
	SourceCurrency string `json:"source_currency"`
	TargetCurrency string `json:"target_currency"`
	SourceAmount   string `json:"source_amount"`
	RequestKey     string `json:"request_key,omitempty"`
}

type fxQuoteHTTPResponse struct {
	ID                uuid.UUID `json:"id"`
	PairID            uuid.UUID `json:"pair_id"`
	DirectionID       uuid.UUID `json:"direction_id"`
	RateVersionID     uuid.UUID `json:"rate_version_id"`
	SourceCurrency    string    `json:"source_currency"`
	TargetCurrency    string    `json:"target_currency"`
	SourceMinorUnit   int16     `json:"source_minor_unit"`
	TargetMinorUnit   int16     `json:"target_minor_unit"`
	SourceAmount      string    `json:"source_amount"`
	TargetAmount      string    `json:"target_amount"`
	ReferenceRate     string    `json:"reference_rate"`
	ClientRate        string    `json:"client_rate"`
	RateConvention    string    `json:"rate_convention"`
	PairPolicyVersion int64     `json:"pair_policy_version"`
	SpreadBasisPoints int64     `json:"spread_basis_points"`
	RoundingMode      string    `json:"rounding_mode"`
	RoundingRemainder string    `json:"rounding_remainder"`
	Status            string    `json:"status"`
	ExpiresAt         string    `json:"expires_at"`
	ConsumedAt        string    `json:"consumed_at,omitempty"`
	CreatedAt         string    `json:"created_at"`
}

type createFXConversionHTTPRequest struct {
	QuoteID              string `json:"quote_id"`
	ExpectedSourceAmount string `json:"expected_source_amount"`
	ExpectedTargetAmount string `json:"expected_target_amount"`
	IdempotencyKey       string `json:"idempotency_key,omitempty"`
}

type fxConversionHTTPResponse struct {
	ID                  uuid.UUID `json:"id"`
	QuoteID             uuid.UUID `json:"quote_id"`
	IdempotencyKey      string    `json:"idempotency_key"`
	SourceCurrency      string    `json:"source_currency"`
	TargetCurrency      string    `json:"target_currency"`
	SourceAmount        string    `json:"source_amount"`
	TargetAmount        string    `json:"target_amount"`
	Status              string    `json:"status"`
	SourceTransactionID uuid.UUID `json:"source_transaction_id,omitempty"`
	TargetTransactionID uuid.UUID `json:"target_transaction_id,omitempty"`
	CreatedAt           string    `json:"created_at"`
	PostedAt            string    `json:"posted_at,omitempty"`
}

type fxPairHTTPResponse struct {
	ID                uuid.UUID                 `json:"id"`
	PairCode          string                    `json:"pair_code"`
	BaseCurrency      string                    `json:"base_currency"`
	QuoteCurrency     string                    `json:"quote_currency"`
	RateConvention    string                    `json:"rate_convention"`
	Status            string                    `json:"status"`
	RateSource        string                    `json:"rate_source"`
	QuoteTTLSeconds   int                       `json:"quote_ttl_seconds"`
	RoundingMode      string                    `json:"rounding_mode"`
	PairPolicyVersion int64                     `json:"pair_policy_version"`
	Directions        []fxDirectionHTTPResponse `json:"directions"`
}

type fxDirectionHTTPResponse struct {
	ID                uuid.UUID `json:"id"`
	SourceCurrency    string    `json:"source_currency"`
	TargetCurrency    string    `json:"target_currency"`
	Enabled           bool      `json:"enabled"`
	NewQuotesPaused   bool      `json:"new_quotes_paused"`
	ConversionsPaused bool      `json:"conversions_paused"`
	MinSourceAmount   string    `json:"min_source_amount"`
	MaxSourceAmount   string    `json:"max_source_amount"`
	SpreadBasisPoints int64     `json:"spread_basis_points"`
}

func (h *handler) multiCurrency() (multiCurrencyService, bool) {
	svc, ok := h.svc.(multiCurrencyService)
	return svc, ok
}

func (h *handler) listCurrencies(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	svc, ok := h.multiCurrency()
	if !ok {
		response.InternalServerError(w, errors.New("multi-currency service is unavailable"))
		return
	}
	items, err := svc.ListCurrencies(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]currencyHTTPResponse, 0, len(items))
	for _, item := range items {
		if item.Status == "draft" {
			continue
		}
		out = append(out, currencyHTTPResponse{
			Code: item.Code, MinorUnit: item.MinorUnit, Status: item.Status,
			Operations: item.Operations, UserEnabled: item.UserEnabled,
		})
	}
	response.OK(w, listCurrenciesHTTPResponse{Currencies: out})
}

func (h *handler) listCurrencyBalances(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	svc, ok := h.multiCurrency()
	if !ok {
		response.InternalServerError(w, errors.New("multi-currency service is unavailable"))
		return
	}
	items, err := svc.ListCurrencyBalances(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]currencyBalanceHTTPResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toCurrencyBalanceHTTPResponse(item))
	}
	response.OK(w, listCurrencyBalancesHTTPResponse{Balances: out})
}

func (h *handler) getCurrencyBalance(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	svc, ok := h.multiCurrency()
	if !ok {
		response.InternalServerError(w, errors.New("multi-currency service is unavailable"))
		return
	}
	item, err := svc.GetCurrencyBalance(r.Context(), userID, r.PathValue("currency"))
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toCurrencyBalanceHTTPResponse(item))
}

func (h *handler) enableCurrency(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	svc, ok := h.multiCurrency()
	if !ok {
		response.InternalServerError(w, errors.New("multi-currency service is unavailable"))
		return
	}
	code := r.PathValue("currency")
	accounts, err := svc.EnableUserCurrency(r.Context(), userID, code)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]accountResponse, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, toAccountResponse(account))
	}
	response.Created(w, enableCurrencyHTTPResponse{Currency: code, Enabled: true, Accounts: out})
}

func (h *handler) listFXPairs(w http.ResponseWriter, r *http.Request) {
	if _, ok := currentUserID(r); !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	svc, ok := h.multiCurrency()
	if !ok {
		response.InternalServerError(w, errors.New("multi-currency service is unavailable"))
		return
	}
	pairs, err := svc.ListFXPairs(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]fxPairHTTPResponse, 0, len(pairs))
	for _, pair := range pairs {
		if pair.Status != "active" {
			continue
		}
		directions := make([]fxDirectionHTTPResponse, 0, len(pair.Directions))
		for _, direction := range pair.Directions {
			directions = append(directions, fxDirectionHTTPResponse{
				ID: direction.ID, SourceCurrency: direction.SourceCurrency,
				TargetCurrency: direction.TargetCurrency, Enabled: direction.Enabled,
				NewQuotesPaused: direction.NewQuotesPaused, ConversionsPaused: direction.ConversionsPaused,
				MinSourceAmount:   strconv.FormatInt(direction.MinSourceAmount, 10),
				MaxSourceAmount:   strconv.FormatInt(direction.MaxSourceAmount, 10),
				SpreadBasisPoints: direction.SpreadBasisPoints,
			})
		}
		out = append(out, fxPairHTTPResponse{
			ID: pair.ID, PairCode: pair.PairCode, BaseCurrency: pair.BaseCurrency, QuoteCurrency: pair.QuoteCurrency,
			RateConvention: pair.RateConvention, Status: pair.Status, RateSource: pair.RateSource, QuoteTTLSeconds: pair.QuoteTTLSeconds,
			RoundingMode: pair.RoundingMode, PairPolicyVersion: pair.PairPolicyVersion,
			Directions: directions,
		})
	}
	response.OK(w, map[string]any{"pairs": out})
}

func (h *handler) createFXQuote(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	svc, ok := h.multiCurrency()
	if !ok {
		response.InternalServerError(w, errors.New("multi-currency service is unavailable"))
		return
	}
	var req createFXQuoteHTTPRequest
	if !response.Decode(w, r, &req) {
		return
	}
	amount, err := parseUnsignedMinor(req.SourceAmount)
	if err != nil {
		response.BadRequest(w, "source_amount must be an unsigned minor-unit integer string")
		return
	}
	requestKey := firstNonEmpty(r.Header.Get("Idempotency-Key"), req.RequestKey)
	quote, err := svc.CreateFXQuote(r.Context(), userID, req.SourceCurrency, req.TargetCurrency, amount, requestKey)
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, toFXQuoteHTTPResponse(quote))
}

func (h *handler) getFXQuote(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	quoteID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "id must be a valid UUID")
		return
	}
	svc, ok := h.multiCurrency()
	if !ok {
		response.InternalServerError(w, errors.New("multi-currency service is unavailable"))
		return
	}
	quote, err := svc.GetFXQuote(r.Context(), userID, quoteID)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toFXQuoteHTTPResponse(quote))
}

func (h *handler) createFXConversion(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	svc, ok := h.multiCurrency()
	if !ok {
		response.InternalServerError(w, errors.New("multi-currency service is unavailable"))
		return
	}
	var req createFXConversionHTTPRequest
	if !response.Decode(w, r, &req) {
		return
	}
	quoteID, err := uuid.Parse(req.QuoteID)
	if err != nil {
		response.BadRequest(w, "quote_id must be a valid UUID")
		return
	}
	if req.ExpectedSourceAmount == "" || req.ExpectedTargetAmount == "" {
		response.BadRequest(w, "expected_source_amount and expected_target_amount are required")
		return
	}
	expectedSource, err := parseUnsignedMinor(req.ExpectedSourceAmount)
	if err != nil {
		response.BadRequest(w, "expected_source_amount must be an unsigned minor-unit integer string")
		return
	}
	expectedTarget, err := parseUnsignedMinor(req.ExpectedTargetAmount)
	if err != nil {
		response.BadRequest(w, "expected_target_amount must be an unsigned minor-unit integer string")
		return
	}
	idempotencyKey := firstNonEmpty(r.Header.Get("Idempotency-Key"), req.IdempotencyKey)
	conversion, err := svc.ExecuteFXConversion(r.Context(), userID, quoteID, idempotencyKey, expectedSource, expectedTarget)
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, toFXConversionHTTPResponse(conversion))
}

func (h *handler) getFXConversion(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	conversionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "id must be a valid UUID")
		return
	}
	svc, ok := h.multiCurrency()
	if !ok {
		response.InternalServerError(w, errors.New("multi-currency service is unavailable"))
		return
	}
	conversion, err := svc.GetFXConversion(r.Context(), userID, conversionID)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toFXConversionHTTPResponse(conversion))
}

func toCurrencyBalanceHTTPResponse(item model.CurrencyBalance) currencyBalanceHTTPResponse {
	return currencyBalanceHTTPResponse{
		Currency: item.Currency, MinorUnit: item.MinorUnit, Status: item.Status,
		Operations: item.Operations, UserEnabled: item.UserEnabled,
		Available: strconv.FormatInt(item.Available, 10), Hold: strconv.FormatInt(item.Hold, 10),
		Pending: strconv.FormatInt(item.Pending, 10), Frozen: strconv.FormatInt(item.Frozen, 10),
	}
}

func toFXQuoteHTTPResponse(item model.FXQuote) fxQuoteHTTPResponse {
	result := fxQuoteHTTPResponse{
		ID: item.ID, PairID: item.PairID, DirectionID: item.DirectionID, RateVersionID: item.RateVersionID,
		SourceCurrency: item.SourceCurrency, TargetCurrency: item.TargetCurrency,
		SourceMinorUnit: item.SourceMinorUnit, TargetMinorUnit: item.TargetMinorUnit,
		SourceAmount: strconv.FormatInt(item.SourceAmount, 10), TargetAmount: strconv.FormatInt(item.TargetAmount, 10),
		ReferenceRate: item.ReferenceRate, ClientRate: item.ClientRate, RateConvention: item.RateConvention,
		PairPolicyVersion: item.PairPolicyVersion,
		SpreadBasisPoints: item.SpreadBasisPoints, RoundingMode: item.RoundingMode,
		RoundingRemainder: item.RoundingRemainder, Status: item.Status,
		ExpiresAt: item.ExpiresAt.UTC().Format(time.RFC3339Nano), CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if item.ConsumedAt != nil {
		result.ConsumedAt = item.ConsumedAt.UTC().Format(time.RFC3339Nano)
	}
	return result
}

func toFXConversionHTTPResponse(item model.FXConversion) fxConversionHTTPResponse {
	result := fxConversionHTTPResponse{
		ID: item.ID, QuoteID: item.QuoteID, IdempotencyKey: item.IdempotencyKey,
		SourceCurrency: item.SourceCurrency, TargetCurrency: item.TargetCurrency,
		SourceAmount: strconv.FormatInt(item.SourceAmount, 10), TargetAmount: strconv.FormatInt(item.TargetAmount, 10),
		Status: item.Status, SourceTransactionID: item.SourceTransactionID,
		TargetTransactionID: item.TargetTransactionID,
		CreatedAt:           item.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if item.PostedAt != nil {
		result.PostedAt = item.PostedAt.UTC().Format(time.RFC3339Nano)
	}
	return result
}

func parseUnsignedMinor(raw string) (int64, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return 0, errors.New("invalid minor amount")
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, errors.New("invalid minor amount")
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, errors.New("invalid minor amount")
	}
	return value, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
