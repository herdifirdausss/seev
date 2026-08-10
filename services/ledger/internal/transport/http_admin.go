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
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	constant "github.com/herdifirdausss/seev/services/ledger/internal/ledger/constants"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

// ─── Admin: outbox dead-letter replay (docs/roadmap/archive/12 Task T3) ──────────────────
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

// listDeadEvents serves GET /admin/outbox/dead?limit=&offset= (docs/roadmap/archive/25
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

// ─── Maker-checker adjustments (docs/roadmap/archive/16 Task T1) ──────────────────────
// Internal router only, admin-gated in every handler — defense in depth on
// top of network isolation, same pattern as the outbox replay endpoints
// above. This is the ONLY reachable path to adjustment_credit/
// adjustment_debit (see governance.IsWorkflowOnlyPostType in postTransaction).

func (h *handler) createAdjustment(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
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
	if req.Type == "fx_rebalance_credit" || req.Type == "fx_rebalance_debit" {
		response.BadRequest(w, "FX rebalances must use /admin/fx/rebalances")
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
	// adjustment_suspense_credit/debit (docs/roadmap/archive/16 Task T2), which target
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
	var referenceID uuid.UUID
	if req.ReferenceID != "" {
		referenceID, err = uuid.Parse(req.ReferenceID)
		if err != nil {
			response.BadRequest(w, "reference_id must be a valid UUID")
			return
		}
	}

	id, err := h.svc.CreateAdjustment(r.Context(), requestedBy.String(), req.Type, amount, targetUserID, referenceID, req.Metadata, req.Reason)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, createAdjustmentResponse{ID: id})
}

func (h *handler) approveAdjustment(w http.ResponseWriter, r *http.Request) {
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
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
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
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

// ─── External reconciliation (docs/roadmap/archive/16 Task T2) ────────────────────────
// Internal router only, admin-gated in every handler — same defense-in-depth
// pattern as the adjustment endpoints above. resolveReconItem creates a
// pending adjustment via the maker-checker path; it never moves money by
// itself (decision K5 step 5).

// maxReconCSVUploadBytes caps the multipart request body — a 50,000-row CSV
// (the service-layer row cap, docs/roadmap/archive/16 Task T2 step 3) at a generous
// worst-case row width fits well under this; a request over the limit is
// rejected outright rather than partially read.
const maxReconCSVUploadBytes = 10 << 20 // 10MiB

// maxReconCSVRows mirrors service/recon's own authoritative cap — checked
// again here so an oversized file is rejected while STREAMING the CSV
// (never fully buffered in memory) rather than only after every row has
// already been parsed and handed to the service layer.
const maxReconCSVRows = 50_000

func (h *handler) createReconBatch(w http.ResponseWriter, r *http.Request) {
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
// (same rule as every other amount in this API, docs/roadmap/archive/10 Task T4).
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
		if errors.Is(err, io.EOF) {
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
// (docs/roadmap/archive/25 Task T5) — lets an operator find a batch's id without SQL
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
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
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
