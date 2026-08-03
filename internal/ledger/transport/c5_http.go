package transport

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	interestservice "github.com/herdifirdausss/seev/internal/ledger/service/interest"
	"github.com/herdifirdausss/seev/pkg/response"
)

type c5SavingsReader interface {
	ListSavingsProducts(context.Context, string) ([]model.SavingsProduct, error)
	GetSavingsProduct(context.Context, uuid.UUID) (model.SavingsProduct, error)
	ListSavingsEnrollments(context.Context, uuid.UUID) ([]model.SavingsEnrollment, error)
	GetSavingsEnrollment(context.Context, uuid.UUID) (model.SavingsEnrollment, error)
	ListInterestAccruals(context.Context, uuid.UUID) ([]model.InterestDailyAccrual, error)
	ListInterestPeriods(context.Context, uuid.UUID) ([]model.InterestPeriod, error)
	ListInterestCapitalizations(context.Context, uuid.UUID) ([]model.InterestCapitalizationItem, error)
}

type c5ScheduleReader interface {
	ListScheduledOccurrences(context.Context, uuid.UUID, uuid.UUID, int, int) ([]model.ScheduledOccurrence, error)
	GetScheduledOccurrence(context.Context, uuid.UUID, uuid.UUID) (model.ScheduledOccurrence, error)
	ListScheduledExecutionAttempts(context.Context, uuid.UUID) ([]model.ScheduledExecutionAttempt, error)
	RetryScheduledOccurrence(context.Context, uuid.UUID) error
	ConfirmScheduledFeeCap(context.Context, uuid.UUID, uuid.UUID, int64) error
}

type c5FeeCapConfirmationRequest struct {
	MaxFeeAmount *int64 `json:"max_fee_amount"`
}

type c5ScheduleCreator interface {
	CreateScheduleWithPolicy(context.Context, uuid.UUID, string, decimal.Decimal, uuid.UUID, string, map[string]any, string, time.Time, *int, string, model.ScheduledPolicy, string, string, string) (uuid.UUID, error)
}

type c5SavingsAdmin interface {
	CreateSavingsProduct(context.Context, model.SavingsProduct) (model.SavingsProduct, error)
	SetSavingsProductStatus(context.Context, uuid.UUID, string, string) (model.SavingsProduct, error)
	CreateSavingsRate(context.Context, model.SavingsRateVersion) (model.SavingsRateVersion, error)
	SubmitSavingsRate(context.Context, uuid.UUID, string) error
	ApproveSavingsRate(context.Context, uuid.UUID, string) error
	RejectSavingsRate(context.Context, uuid.UUID, string, string) error
	EnrollSavingsAccount(context.Context, model.SavingsEnrollment) (model.SavingsEnrollment, error)
	PauseSavingsEnrollment(context.Context, uuid.UUID, string) error
	ResumeSavingsEnrollment(context.Context, uuid.UUID, string) error
	EndSavingsEnrollment(context.Context, uuid.UUID, string) error
	GetInterestPeriod(context.Context, uuid.UUID) (model.InterestPeriod, error)
	PreviewInterestPeriodClose(context.Context, uuid.UUID) (interestservice.PeriodPreview, error)
	RunInterestPeriodClose(context.Context, uuid.UUID, string) error
	RunInterestDaily(context.Context, time.Time) interestservice.DailyRunSummary
	RetryInterestPeriodItem(context.Context, uuid.UUID) error
	CreateInterestAdjustment(context.Context, model.InterestAdjustment) (model.InterestAdjustment, error)
	ApproveInterestAdjustment(context.Context, uuid.UUID, string) error
}

type c5ProductResponse struct {
	ID                      uuid.UUID `json:"id"`
	PublicID                string    `json:"public_id"`
	ProductCode             string    `json:"product_code"`
	Name                    string    `json:"name"`
	Currency                string    `json:"currency"`
	EligibleAccountTypes    []string  `json:"eligible_account_types"`
	Status                  string    `json:"status"`
	DayCountConvention      string    `json:"day_count_convention"`
	CapitalizationFrequency string    `json:"capitalization_frequency"`
	Timezone                string    `json:"timezone"`
	MinimumEligibleBalance  int64     `json:"minimum_eligible_balance"`
	Version                 int64     `json:"version"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

func toC5ProductResponse(product model.SavingsProduct) c5ProductResponse {
	return c5ProductResponse{
		ID: product.ID, PublicID: product.PublicID, ProductCode: product.ProductCode,
		Name: product.Name, Currency: product.Currency, EligibleAccountTypes: product.EligibleAccountTypes,
		Status: product.Status, DayCountConvention: product.DayCountConvention,
		CapitalizationFrequency: product.CapitalizationFrequency, Timezone: product.Timezone,
		MinimumEligibleBalance: product.MinimumEligibleBalance, Version: product.Version,
		CreatedAt: product.CreatedAt, UpdatedAt: product.UpdatedAt,
	}
}

type c5EnrollmentResponse struct {
	ID               uuid.UUID `json:"id"`
	PublicID         string    `json:"public_id"`
	ProductID        uuid.UUID `json:"product_id"`
	AccountID        uuid.UUID `json:"account_id"`
	UserID           uuid.UUID `json:"user_id"`
	Status           string    `json:"status"`
	Mode             string    `json:"mode"`
	EffectiveFrom    string    `json:"effective_from"`
	EffectiveUntil   *string   `json:"effective_until,omitempty"`
	CarryNumerator   string    `json:"carry_numerator"`
	CarryDenominator string    `json:"carry_denominator"`
	Version          int64     `json:"version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func toC5EnrollmentResponse(enrollment model.SavingsEnrollment) c5EnrollmentResponse {
	out := c5EnrollmentResponse{
		ID: enrollment.ID, PublicID: enrollment.PublicID, ProductID: enrollment.ProductID,
		AccountID: enrollment.AccountID, UserID: enrollment.UserID, Status: enrollment.Status,
		Mode: enrollment.Mode, EffectiveFrom: enrollment.EffectiveFrom.Format("2006-01-02"),
		CarryNumerator: enrollment.CarryNumerator, CarryDenominator: enrollment.CarryDenominator, Version: enrollment.Version,
		CreatedAt: enrollment.CreatedAt, UpdatedAt: enrollment.UpdatedAt,
	}
	if enrollment.EffectiveUntil != nil {
		value := enrollment.EffectiveUntil.Format("2006-01-02")
		out.EffectiveUntil = &value
	}
	return out
}

type c5OccurrenceResponse struct {
	ID                 uuid.UUID           `json:"id"`
	PublicID           string              `json:"public_id"`
	ScheduleID         uuid.UUID           `json:"schedule_id"`
	ScheduleVersion    int64               `json:"schedule_version"`
	ScheduledFor       time.Time           `json:"scheduled_for"`
	ScheduledLocalDate string              `json:"scheduled_local_date"`
	Status             string              `json:"status"`
	IdempotencyKey     string              `json:"idempotency_key"`
	PolicySnapshot     any                 `json:"policy_snapshot"`
	FeeAmount          *int64              `json:"fee_amount,omitempty"`
	FeeQuoteID         *uuid.UUID          `json:"fee_quote_id,omitempty"`
	LedgerTransaction  *uuid.UUID          `json:"ledger_transaction_id,omitempty"`
	AttemptCount       int                 `json:"attempt_count"`
	NextAttemptAt      *time.Time          `json:"next_attempt_at,omitempty"`
	ErrorCode          *string             `json:"error_code,omitempty"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
	Attempts           []c5AttemptResponse `json:"attempts,omitempty"`
}

type c5AttemptResponse struct {
	ID                  uuid.UUID  `json:"id"`
	AttemptNumber       int        `json:"attempt_number"`
	Phase               string     `json:"phase"`
	Result              string     `json:"result"`
	Retryable           bool       `json:"retryable"`
	ErrorCode           *string    `json:"error_code,omitempty"`
	LedgerTransactionID *uuid.UUID `json:"ledger_transaction_id,omitempty"`
	StartedAt           time.Time  `json:"started_at"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
}

func toC5OccurrenceResponse(item model.ScheduledOccurrence) c5OccurrenceResponse {
	return c5OccurrenceResponse{
		ID: item.ID, PublicID: item.PublicID, ScheduleID: item.ScheduleID,
		ScheduleVersion: item.ScheduleVersion, ScheduledFor: item.ScheduledFor,
		ScheduledLocalDate: item.ScheduledLocalDate.Format("2006-01-02"), Status: item.Status,
		IdempotencyKey: item.IdempotencyKey, PolicySnapshot: item.PolicySnapshot,
		FeeAmount: item.FeeAmount, FeeQuoteID: item.FeeQuoteID,
		LedgerTransaction: item.LedgerTransactionID, AttemptCount: item.AttemptCount,
		NextAttemptAt: item.NextAttemptAt, ErrorCode: item.ErrorCode,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

func toC5AttemptResponse(item model.ScheduledExecutionAttempt) c5AttemptResponse {
	return c5AttemptResponse{ID: item.ID, AttemptNumber: item.AttemptNumber, Phase: item.Phase,
		Result: item.Result, Retryable: item.Retryable, ErrorCode: item.ErrorCode,
		LedgerTransactionID: item.LedgerTransactionID, StartedAt: item.StartedAt, FinishedAt: item.FinishedAt}
}

func attachC5Attempts(ctx context.Context, reader c5ScheduleReader, items []model.ScheduledOccurrence) ([]c5OccurrenceResponse, error) {
	out := make([]c5OccurrenceResponse, 0, len(items))
	for _, item := range items {
		responseItem := toC5OccurrenceResponse(item)
		attempts, err := reader.ListScheduledExecutionAttempts(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		responseItem.Attempts = make([]c5AttemptResponse, 0, len(attempts))
		for _, attempt := range attempts {
			responseItem.Attempts = append(responseItem.Attempts, toC5AttemptResponse(attempt))
		}
		out = append(out, responseItem)
	}
	return out, nil
}

type listC5ProductsResponse struct {
	Products []c5ProductResponse `json:"products"`
}
type listC5EnrollmentsResponse struct {
	Enrollments []c5EnrollmentResponse `json:"enrollments"`
}
type listC5OccurrencesResponse struct {
	Occurrences []c5OccurrenceResponse `json:"occurrences"`
}

type c5AccrualResponse struct {
	Date                    string `json:"date"`
	ClosingBalance          *int64 `json:"closing_balance,omitempty"`
	RateBPS                 *int   `json:"rate_bps,omitempty"`
	RecognizedAccruedAmount *int64 `json:"recognized_accrued_amount,omitempty"`
	CapitalizationStatus    string `json:"capitalization_status"`
	Currency                string `json:"currency"`
	Status                  string `json:"status"`
}

type c5PeriodResponse struct {
	ID                       uuid.UUID  `json:"id"`
	Currency                 string     `json:"currency"`
	PeriodYear               int        `json:"period_year"`
	PeriodMonth              int        `json:"period_month"`
	PeriodStartAt            time.Time  `json:"period_start_at"`
	PeriodEndAt              time.Time  `json:"period_end_at"`
	CloseNotBeforeAt         time.Time  `json:"close_not_before_at"`
	Status                   string     `json:"status"`
	ExpectedItemCount        int64      `json:"expected_item_count"`
	CompletedItemCount       int64      `json:"completed_item_count"`
	BlockedItemCount         int64      `json:"blocked_item_count"`
	TotalAccruedAmount       int64      `json:"total_accrued_amount"`
	TotalCapitalizedAmount   int64      `json:"total_capitalized_amount"`
	AccruedNotYetCapitalized int64      `json:"accrued_not_yet_capitalized"`
	ClosedAt                 *time.Time `json:"closed_at,omitempty"`
}

type listC5AccrualsResponse struct {
	Accruals []c5AccrualResponse `json:"accruals"`
}
type listC5PeriodsResponse struct {
	Periods []c5PeriodResponse `json:"periods"`
}

type c5ProductRequest struct {
	ProductCode            string   `json:"product_code"`
	Name                   string   `json:"name"`
	Currency               string   `json:"currency"`
	EligibleAccountTypes   []string `json:"eligible_account_types"`
	Status                 string   `json:"status"`
	Timezone               string   `json:"timezone"`
	MinimumEligibleBalance int64    `json:"minimum_eligible_balance"`
	InterestExpenseAccount string   `json:"interest_expense_account_id"`
	InterestPayableAccount string   `json:"interest_payable_account_id"`
}

type c5ProductStatusRequest struct {
	Status string `json:"status"`
}

type c5RateRequest struct {
	AnnualRateBps  int    `json:"annual_rate_bps"`
	EffectiveFrom  string `json:"effective_from"`
	EffectiveUntil string `json:"effective_until,omitempty"`
	ContentHash    string `json:"content_hash,omitempty"`
}

type c5EnrollmentRequest struct {
	ProductID      string `json:"product_id"`
	AccountID      string `json:"account_id"`
	UserID         string `json:"user_id"`
	Mode           string `json:"mode"`
	EffectiveFrom  string `json:"effective_from"`
	EffectiveUntil string `json:"effective_until,omitempty"`
}

type c5AdjustmentRequest struct {
	PeriodID         string `json:"period_id"`
	EnrollmentID     string `json:"enrollment_id"`
	AccrualID        string `json:"source_accrual_id,omitempty"`
	CapitalizationID string `json:"source_capitalization_id,omitempty"`
	Amount           string `json:"amount"`
	Direction        string `json:"direction"`
	Reason           string `json:"reason"`
}

func (h *handler) listC5SavingsProducts(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.svc.(c5SavingsReader)
	if !ok {
		response.JSON(w, http.StatusNotImplemented, map[string]string{"code": "C5_UNAVAILABLE"})
		return
	}
	status := "active"
	if h.allowedTypes == nil {
		status = r.URL.Query().Get("status")
	}
	products, err := reader.ListSavingsProducts(r.Context(), status)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]c5ProductResponse, len(products))
	for i, product := range products {
		out[i] = toC5ProductResponse(product)
	}
	response.OK(w, listC5ProductsResponse{Products: out})
}

func (h *handler) listC5SavingsEnrollments(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	reader, ok := h.svc.(c5SavingsReader)
	if !ok {
		response.JSON(w, http.StatusNotImplemented, map[string]string{"code": "C5_UNAVAILABLE"})
		return
	}
	enrollments, err := reader.ListSavingsEnrollments(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]c5EnrollmentResponse, len(enrollments))
	for i, enrollment := range enrollments {
		out[i] = toC5EnrollmentResponse(enrollment)
	}
	response.OK(w, listC5EnrollmentsResponse{Enrollments: out})
}

func (h *handler) getC5SavingsEnrollment(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid enrollment id")
		return
	}
	reader, ok := h.svc.(c5SavingsReader)
	if !ok {
		response.JSON(w, http.StatusNotImplemented, map[string]string{"code": "C5_UNAVAILABLE"})
		return
	}
	enrollment, err := reader.GetSavingsEnrollment(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if enrollment.UserID != userID {
		response.NotFound(w, "savings enrollment not found")
		return
	}
	response.OK(w, toC5EnrollmentResponse(enrollment))
}

func (h *handler) listC5SavingsAccruals(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid enrollment id")
		return
	}
	reader, ok := h.svc.(c5SavingsReader)
	if !ok {
		response.JSON(w, http.StatusNotImplemented, map[string]string{"code": "C5_UNAVAILABLE"})
		return
	}
	enrollment, err := reader.GetSavingsEnrollment(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if enrollment.UserID != userID {
		response.NotFound(w, "savings enrollment not found")
		return
	}
	product, err := reader.GetSavingsProduct(r.Context(), enrollment.ProductID)
	if err != nil {
		writeError(w, err)
		return
	}
	accruals, err := reader.ListInterestAccruals(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	capitalizations, err := reader.ListInterestCapitalizations(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	capitalizationStatus := make(map[uuid.UUID]string, len(capitalizations))
	for _, item := range capitalizations {
		capitalizationStatus[item.PeriodID] = item.Status
	}
	out := make([]c5AccrualResponse, 0, len(accruals))
	for _, item := range accruals {
		status := capitalizationStatus[item.PeriodID]
		if status == "" {
			status = "not_created"
		}
		out = append(out, c5AccrualResponse{
			Date: item.AccrualDate.Format("2006-01-02"), ClosingBalance: item.ClosingBalance,
			RateBPS: item.AnnualRateBps, RecognizedAccruedAmount: item.RecognizedAmount,
			CapitalizationStatus: status, Currency: product.Currency, Status: item.Status,
		})
	}
	response.OK(w, listC5AccrualsResponse{Accruals: out})
}

func (h *handler) listC5SavingsPeriods(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid enrollment id")
		return
	}
	reader, ok := h.svc.(c5SavingsReader)
	if !ok {
		response.JSON(w, http.StatusNotImplemented, map[string]string{"code": "C5_UNAVAILABLE"})
		return
	}
	enrollment, err := reader.GetSavingsEnrollment(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if enrollment.UserID != userID {
		response.NotFound(w, "savings enrollment not found")
		return
	}
	periods, err := reader.ListInterestPeriods(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]c5PeriodResponse, 0, len(periods))
	for _, period := range periods {
		pending := max(period.TotalAccruedAmount-period.TotalCapitalizedAmount, 0)
		out = append(out, c5PeriodResponse{
			ID: period.ID, Currency: period.Currency, PeriodYear: period.PeriodYear,
			PeriodMonth: period.PeriodMonth, PeriodStartAt: period.PeriodStartAt,
			PeriodEndAt: period.PeriodEndAt, CloseNotBeforeAt: period.CloseNotBeforeAt,
			Status: period.Status, ExpectedItemCount: period.ExpectedItemCount,
			CompletedItemCount: period.CompletedItemCount, BlockedItemCount: period.BlockedItemCount,
			TotalAccruedAmount:       period.TotalAccruedAmount,
			TotalCapitalizedAmount:   period.TotalCapitalizedAmount,
			AccruedNotYetCapitalized: pending, ClosedAt: period.ClosedAt,
		})
	}
	response.OK(w, listC5PeriodsResponse{Periods: out})
}

func (h *handler) listScheduledOccurrences(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	scheduleID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid schedule id")
		return
	}
	reader, ok := h.svc.(c5ScheduleReader)
	if !ok {
		response.JSON(w, http.StatusNotImplemented, map[string]string{"code": "C5_UNAVAILABLE"})
		return
	}
	limit, offset := c5Pagination(r)
	items, err := reader.ListScheduledOccurrences(r.Context(), scheduleID, userID, limit, offset)
	if err != nil {
		writeError(w, err)
		return
	}
	writeC5Occurrences(w, r.Context(), reader, items)
}

func (h *handler) getScheduledOccurrence(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	rawID := r.PathValue("occurrence_id")
	if rawID == "" {
		rawID = r.PathValue("execution_id")
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		response.BadRequest(w, "invalid occurrence id")
		return
	}
	reader, ok := h.svc.(c5ScheduleReader)
	if !ok {
		response.JSON(w, http.StatusNotImplemented, map[string]string{"code": "C5_UNAVAILABLE"})
		return
	}
	item, err := reader.GetScheduledOccurrence(r.Context(), id, userID)
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := attachC5Attempts(r.Context(), reader, []model.ScheduledOccurrence{item})
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, items[0])
}

func (h *handler) listAdminScheduledOccurrences(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	scheduleID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid schedule id")
		return
	}
	reader, ok := h.svc.(c5ScheduleReader)
	if !ok {
		response.JSON(w, http.StatusNotImplemented, map[string]string{"code": "C5_UNAVAILABLE"})
		return
	}
	limit, offset := c5Pagination(r)
	items, err := reader.ListScheduledOccurrences(r.Context(), scheduleID, uuid.Nil, limit, offset)
	if err != nil {
		writeError(w, err)
		return
	}
	writeC5Occurrences(w, r.Context(), reader, items)
}

func (h *handler) retryScheduledOccurrence(w http.ResponseWriter, r *http.Request) {
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	id, err := uuid.Parse(r.PathValue("occurrence_id"))
	if err != nil {
		response.BadRequest(w, "invalid occurrence id")
		return
	}
	reader, ok := h.svc.(c5ScheduleReader)
	if !ok {
		response.JSON(w, http.StatusNotImplemented, map[string]string{"code": "C5_UNAVAILABLE"})
		return
	}
	if err := reader.RetryScheduledOccurrence(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, map[string]bool{"retry_queued": true})
}

func (h *handler) confirmScheduledFeeCap(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	scheduleID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid schedule id")
		return
	}
	var req c5FeeCapConfirmationRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.MaxFeeAmount == nil || *req.MaxFeeAmount < 0 {
		response.BadRequest(w, "max_fee_amount must be a non-negative integer")
		return
	}
	reader, ok := h.svc.(c5ScheduleReader)
	if !ok {
		response.JSON(w, http.StatusNotImplemented, map[string]string{"code": "C5_UNAVAILABLE"})
		return
	}
	if err := reader.ConfirmScheduledFeeCap(r.Context(), scheduleID, userID, *req.MaxFeeAmount); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, map[string]any{"confirmed": true, "max_fee_amount": *req.MaxFeeAmount})
}

func writeC5Occurrences(w http.ResponseWriter, ctx context.Context, reader c5ScheduleReader, items []model.ScheduledOccurrence) {
	out, err := attachC5Attempts(ctx, reader, items)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, listC5OccurrencesResponse{Occurrences: out})
}

func c5Pagination(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func parseC5Date(raw string, required bool) (time.Time, error) {
	if raw == "" && !required {
		return time.Time{}, nil
	}
	value, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: date must be YYYY-MM-DD", apperror.ErrValidation)
	}
	return value, nil
}

func parseC5UUID(raw, field string, required bool) (uuid.UUID, error) {
	if raw == "" && !required {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %s must be a UUID", apperror.ErrValidation, field)
	}
	return id, nil
}

func (h *handler) c5AdminService(w http.ResponseWriter) (c5SavingsAdmin, bool) {
	admin, ok := h.svc.(c5SavingsAdmin)
	if !ok {
		response.JSON(w, http.StatusNotImplemented, map[string]string{"code": "C5_UNAVAILABLE"})
	}
	return admin, ok
}

func (h *handler) createC5SavingsProduct(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	actor, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	admin, ok := h.c5AdminService(w)
	if !ok {
		return
	}
	var req c5ProductRequest
	if !response.Decode(w, r, &req) {
		return
	}
	expenseID, err := parseC5UUID(req.InterestExpenseAccount, "interest_expense_account_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	payableID, err := parseC5UUID(req.InterestPayableAccount, "interest_payable_account_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	product, err := admin.CreateSavingsProduct(r.Context(), model.SavingsProduct{
		ProductCode: req.ProductCode, Name: req.Name, Currency: req.Currency,
		EligibleAccountTypes: req.EligibleAccountTypes, Status: req.Status,
		Timezone: req.Timezone, MinimumEligibleBalance: req.MinimumEligibleBalance,
		InterestExpenseAccountID: expenseID, InterestPayableAccountID: payableID,
		CreatedBy: actor.String(), UpdatedBy: actor.String(),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, toC5ProductResponse(product))
}

func (h *handler) updateC5SavingsProductStatus(w http.ResponseWriter, r *http.Request) {
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	actor, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	admin, ok := h.c5AdminService(w)
	if !ok {
		return
	}
	id, err := parseC5UUID(r.PathValue("id"), "product_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	var req c5ProductStatusRequest
	if !response.Decode(w, r, &req) {
		return
	}
	product, err := admin.SetSavingsProductStatus(r.Context(), id, req.Status, actor.String())
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toC5ProductResponse(product))
}

func (h *handler) createC5SavingsRate(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	actor, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	admin, ok := h.c5AdminService(w)
	if !ok {
		return
	}
	productID, err := parseC5UUID(r.PathValue("id"), "product_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	var req c5RateRequest
	if !response.Decode(w, r, &req) {
		return
	}
	from, err := parseC5Date(req.EffectiveFrom, true)
	if err != nil {
		writeError(w, err)
		return
	}
	until, err := parseC5Date(req.EffectiveUntil, false)
	if err != nil {
		writeError(w, err)
		return
	}
	var contentHash []byte
	if req.ContentHash != "" {
		contentHash = []byte(req.ContentHash)
	}
	rate, err := admin.CreateSavingsRate(r.Context(), model.SavingsRateVersion{ProductID: productID, AnnualRateBps: req.AnnualRateBps, EffectiveFrom: from, EffectiveUntil: timePtr(until), ContentHash: contentHash, CreatedBy: actor.String()})
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, rate)
}

func (h *handler) submitC5SavingsRate(w http.ResponseWriter, r *http.Request) {
	h.applyC5RateAction(w, r, "submit")
}
func (h *handler) approveC5SavingsRate(w http.ResponseWriter, r *http.Request) {
	h.applyC5RateAction(w, r, "approve")
}
func (h *handler) rejectC5SavingsRate(w http.ResponseWriter, r *http.Request) {
	h.applyC5RateAction(w, r, "reject")
}

func (h *handler) applyC5RateAction(w http.ResponseWriter, r *http.Request, action string) {
	if (action == "submit" && !isAdminMaker(r)) || (action != "submit" && !isAdminChecker(r)) {
		response.Forbidden(w, "maker/checker privileges required")
		return
	}
	actor, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	admin, ok := h.c5AdminService(w)
	if !ok {
		return
	}
	id, err := parseC5UUID(r.PathValue("id"), "rate_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	switch action {
	case "submit":
		err = admin.SubmitSavingsRate(r.Context(), id, actor.String())
	case "approve":
		err = admin.ApproveSavingsRate(r.Context(), id, actor.String())
	case "reject":
		var req struct {
			Reason string `json:"reason"`
		}
		if !response.Decode(w, r, &req) {
			return
		}
		err = admin.RejectSavingsRate(r.Context(), id, actor.String(), req.Reason)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	resultKey := map[string]string{"submit": "submitted", "approve": "approved", "reject": "rejected"}[action]
	if resultKey == "" {
		resultKey = "applied"
	}
	response.OK(w, map[string]bool{resultKey: true})
}

func (h *handler) createC5SavingsEnrollment(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	actor, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	admin, ok := h.c5AdminService(w)
	if !ok {
		return
	}
	var req c5EnrollmentRequest
	if !response.Decode(w, r, &req) {
		return
	}
	productID, err := parseC5UUID(req.ProductID, "product_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	accountID, err := parseC5UUID(req.AccountID, "account_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	userID, err := parseC5UUID(req.UserID, "user_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	from, err := parseC5Date(req.EffectiveFrom, true)
	if err != nil {
		writeError(w, err)
		return
	}
	until, err := parseC5Date(req.EffectiveUntil, false)
	if err != nil {
		writeError(w, err)
		return
	}
	enrollment, err := admin.EnrollSavingsAccount(r.Context(), model.SavingsEnrollment{ProductID: productID, AccountID: accountID, UserID: userID, Mode: req.Mode, EffectiveFrom: from, EffectiveUntil: timePtr(until), CreatedBy: actor.String(), UpdatedBy: actor.String()})
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, toC5EnrollmentResponse(enrollment))
}

func (h *handler) pauseC5SavingsEnrollment(w http.ResponseWriter, r *http.Request) {
	h.applyC5EnrollmentStatus(w, r, model.SavingsEnrollmentAccrualPaused)
}

func (h *handler) resumeC5SavingsEnrollment(w http.ResponseWriter, r *http.Request) {
	h.applyC5EnrollmentStatus(w, r, model.SavingsEnrollmentActive)
}

func (h *handler) endC5SavingsEnrollment(w http.ResponseWriter, r *http.Request) {
	h.applyC5EnrollmentStatus(w, r, model.SavingsEnrollmentEnded)
}

func (h *handler) applyC5EnrollmentStatus(w http.ResponseWriter, r *http.Request, status string) {
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	actor, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	admin, ok := h.c5AdminService(w)
	if !ok {
		return
	}
	id, err := parseC5UUID(r.PathValue("id"), "enrollment_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	switch status {
	case model.SavingsEnrollmentAccrualPaused:
		err = admin.PauseSavingsEnrollment(r.Context(), id, actor.String())
	case model.SavingsEnrollmentActive:
		err = admin.ResumeSavingsEnrollment(r.Context(), id, actor.String())
	case model.SavingsEnrollmentEnded:
		err = admin.EndSavingsEnrollment(r.Context(), id, actor.String())
	default:
		err = fmt.Errorf("%w: unsupported enrollment status action", apperror.ErrValidation)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, map[string]string{"status": status})
}

func (h *handler) previewC5InterestPeriod(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	admin, ok := h.c5AdminService(w)
	if !ok {
		return
	}
	id, err := parseC5UUID(r.PathValue("id"), "period_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	preview, err := admin.PreviewInterestPeriodClose(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, preview)
}

func (h *handler) closeC5InterestPeriod(w http.ResponseWriter, r *http.Request) {
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	actor, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	admin, ok := h.c5AdminService(w)
	if !ok {
		return
	}
	id, err := parseC5UUID(r.PathValue("id"), "period_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := admin.RunInterestPeriodClose(r.Context(), id, actor.String()); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, map[string]bool{"closed": true})
}

func (h *handler) runC5InterestDaily(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	admin, ok := h.c5AdminService(w)
	if !ok {
		return
	}
	date := time.Now()
	if raw := r.URL.Query().Get("date"); raw != "" {
		parsed, err := parseC5Date(raw, true)
		if err != nil {
			writeError(w, err)
			return
		}
		date = parsed
	}
	response.OK(w, admin.RunInterestDaily(r.Context(), date))
}

func (h *handler) retryC5InterestPeriodItem(w http.ResponseWriter, r *http.Request) {
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	admin, ok := h.c5AdminService(w)
	if !ok {
		return
	}
	id, err := parseC5UUID(r.PathValue("id"), "period_item_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := admin.RetryInterestPeriodItem(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, map[string]bool{"retry_queued": true})
}

func (h *handler) createC5InterestAdjustment(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	actor, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	admin, ok := h.c5AdminService(w)
	if !ok {
		return
	}
	var req c5AdjustmentRequest
	if !response.Decode(w, r, &req) {
		return
	}
	periodID, err := parseC5UUID(req.PeriodID, "period_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	enrollmentID, err := parseC5UUID(req.EnrollmentID, "enrollment_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	accrualID, err := parseC5UUID(req.AccrualID, "source_accrual_id", false)
	if err != nil {
		writeError(w, err)
		return
	}
	capitalizationID, err := parseC5UUID(req.CapitalizationID, "source_capitalization_id", false)
	if err != nil {
		writeError(w, err)
		return
	}
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || !amount.IsPositive() || !amount.Equal(amount.Truncate(0)) || !amount.BigInt().IsInt64() {
		response.BadRequest(w, "amount must be a positive integer decimal string")
		return
	}
	adjustment, err := admin.CreateInterestAdjustment(r.Context(), model.InterestAdjustment{SourcePeriodID: periodID, EnrollmentID: enrollmentID, SourceAccrualID: uuidPtr(accrualID), SourceCapitalizationID: uuidPtr(capitalizationID), Amount: amount.IntPart(), Direction: req.Direction, Reason: req.Reason, CreatedBy: actor.String()})
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, adjustment)
}

func (h *handler) approveC5InterestAdjustment(w http.ResponseWriter, r *http.Request) {
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	actor, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	admin, ok := h.c5AdminService(w)
	if !ok {
		return
	}
	id, err := parseC5UUID(r.PathValue("id"), "adjustment_id", true)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := admin.ApproveInterestAdjustment(r.Context(), id, actor.String()); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, map[string]bool{"approved": true})
}

func timePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}
func uuidPtr(value uuid.UUID) *uuid.UUID {
	if value == uuid.Nil {
		return nil
	}
	return &value
}
