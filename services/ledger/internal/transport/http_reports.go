package transport

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

// ─── Interest accrual (docs/roadmap/archive/19 Task T3) ────────────────────────────────
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

// ─── AML/fraud screening (docs/roadmap/archive/20 Task T1) ─────────────────────────────
// Internal router only, admin-gated — same defense-in-depth pattern as
// every other admin surface above. Read-only: this is compliance/ops
// visibility into what the screening hooks found, never a write path.

// ─── Regulatory reporting (docs/roadmap/archive/20 Task T2) ────────────────────────────
// Internal router only, admin-gated. Purely read-only over the
// migrations/000018 views — zero write path. format=csv streams row by row
// (encoding/csv straight to the response), same pattern as
// writeStatementCSV (docs/roadmap/archive/15 Task T2) — never fully buffered.

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
