package transport

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/money/currency"
	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

// ─── Scheduled transactions (docs/roadmap/archive/19 Task T1) ──────────────────────────
// createSchedule/listSchedules/pauseSchedule/resumeSchedule/cancelSchedule
// are on the PUBLIC router — a user manages only their own schedules
// (ownership enforced in services/ledger/internal/ledger/schedule, same
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

	var id uuid.UUID
	var createErr error
	financialProductScheduleRequested := req.Currency != "" || req.Timezone != "" || req.LocalTime != "" || req.MissedRunPolicy != "" || req.CatchUpLimit != 0 || req.MaxFeeAmount != nil || req.MaxInfrastructureAttempts != 0 || req.RetryWindowSeconds != 0 || req.FeeMode != "" || req.ConsecutiveFailureThreshold != 0
	if financialProductScheduleRequested {
		creator, ok := h.svc.(scheduleCreator)
		if !ok {
			if req.Currency == "" {
				response.JSON(w, http.StatusNotImplemented, map[string]string{"code": "C5_UNAVAILABLE"})
				return
			}
			currencyCreator, currencyOK := h.svc.(scheduleCurrencyCreator)
			if !currencyOK {
				response.InternalServerError(w, errors.New("currency-aware schedule service is unavailable"))
				return
			}
			id, createErr = currencyCreator.CreateScheduleWithCurrency(r.Context(), userID, req.Type, amount, targetUserID, req.PocketCode, req.Metadata,
				req.ScheduleKind, runAtDate, req.DayOfMonth, userID.String(), req.Currency)
		} else {
			id, createErr = creator.CreateScheduleWithPolicy(r.Context(), userID, req.Type, amount, targetUserID, req.PocketCode, req.Metadata,
				req.ScheduleKind, runAtDate, req.DayOfMonth, userID.String(), model.ScheduledPolicy{
					MissedRunPolicy: req.MissedRunPolicy, CatchUpLimit: req.CatchUpLimit,
					MaxFeeAmount: req.MaxFeeAmount, MaxInfrastructureAttempts: req.MaxInfrastructureAttempts,
					RetryWindowSeconds: req.RetryWindowSeconds, FeeMode: req.FeeMode,
					ConsecutiveFailureThreshold: req.ConsecutiveFailureThreshold,
				}, req.Currency, req.Timezone, req.LocalTime)
		}
	} else {
		id, createErr = h.svc.CreateSchedule(r.Context(), userID, req.Type, amount, targetUserID, req.PocketCode, req.Metadata,
			req.ScheduleKind, runAtDate, req.DayOfMonth, userID.String())
	}
	if createErr != nil {
		writeError(w, createErr)
		return
	}
	created := createScheduleResponse{ID: id}
	if financialProductScheduleRequested {
		created.Timezone = req.Timezone
		if created.Timezone == "" {
			created.Timezone = "Asia/Jakarta"
		}
		created.LocalTime = req.LocalTime
		if created.LocalTime == "" {
			created.LocalTime = "00:30"
		}
		created.MissedRunPolicy = req.MissedRunPolicy
		if created.MissedRunPolicy == "" {
			if req.ScheduleKind == "daily" {
				created.MissedRunPolicy = "skip"
			} else {
				created.MissedRunPolicy = "run_once_latest"
			}
		}
		created.CatchUpLimit = req.CatchUpLimit
		if created.CatchUpLimit == 0 {
			created.CatchUpLimit = 7
		}
		created.MaxFeeAmount = req.MaxFeeAmount
		created.MaxInfrastructureAttempts = req.MaxInfrastructureAttempts
		if created.MaxInfrastructureAttempts == 0 {
			created.MaxInfrastructureAttempts = 5
		}
		created.RetryWindowSeconds = req.RetryWindowSeconds
		if created.RetryWindowSeconds == 0 {
			created.RetryWindowSeconds = 86400
		}
		created.FeeMode = req.FeeMode
		if created.FeeMode == "" {
			created.FeeMode = "current_policy_with_consent_cap"
		}
		created.ConsecutiveFailureThreshold = req.ConsecutiveFailureThreshold
		if created.ConsecutiveFailureThreshold == 0 {
			created.ConsecutiveFailureThreshold = 3
		}
	}
	response.Created(w, created)
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
// (docs/roadmap/archive/19 Task T1 step 5) — internal router only, admin-gated. date
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

// ─── Batch disbursement (docs/roadmap/archive/19 Task T2) ──────────────────────────────
// Internal router only, admin-gated in every handler — same defense-in-depth
// pattern as recon/adjustments above. Import only persists items ('pending');
// Run is the ONLY execution path, called repeatedly (by ops/a script) until
// Done — there is no separate "resume" endpoint, since calling Run again
// after a partial run already only re-selects items still pending/failed.

func (h *handler) createDisbursementBatch(w http.ResponseWriter, r *http.Request) {
	// Business-completeness audit finding: maker-gated the same as
	// createAdjustment — importing a batch alone must never move money, so
	// the identity that imports it can never also be the one whose
	// privileges alone unlock Run (see approveDisbursementBatch below).
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
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
		response.BadRequest(w, "file is required (multipart field 'file', CSV columns: user_id,amount,currency,note)")
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

// parseDisbursementCSV streams user_id,amount,currency,note rows — header order is
// flexible (matched by name), amount is validated integral minor-unit
// before it ever reaches the service layer, and "currency"/"note" are optional.
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
	currencyIdx, hasCurrency := col["currency"]

	var rows []model.DisbursementImportRow
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
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
		currencyCode := "IDR"
		if hasCurrency {
			currencyCode = strings.TrimSpace(rec[currencyIdx])
			if err := currency.ValidateCode(currencyCode); err != nil {
				return nil, fmt.Errorf("row %d: invalid currency %q: %w", len(rows)+2, currencyCode, err)
			}
		}
		rows = append(rows, model.DisbursementImportRow{UserID: userID, Amount: amount, Currency: currencyCode, Note: note})
	}
	return rows, nil
}

// approveDisbursementBatch/rejectDisbursementBatch back the maker-checker
// gate a business-completeness audit finding added: Import alone never
// moves money, and Run refuses to process a single item until a DIFFERENT
// identity (checker privileges, same as approveAdjustment) approves the
// batch here.
func (h *handler) approveDisbursementBatch(w http.ResponseWriter, r *http.Request) {
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	approverID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	batchID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid batch id")
		return
	}

	if err := h.svc.ApproveDisbursementBatch(r.Context(), batchID, approverID.String()); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, approveDisbursementBatchResponse{Approved: true})
}

func (h *handler) rejectDisbursementBatch(w http.ResponseWriter, r *http.Request) {
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	approverID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	batchID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid batch id")
		return
	}
	var req rejectDisbursementBatchRequest
	if !response.Decode(w, r, &req) {
		return
	}

	if err := h.svc.RejectDisbursementBatch(r.Context(), batchID, approverID.String(), req.Reason); err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, rejectDisbursementBatchResponse{Rejected: true})
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
