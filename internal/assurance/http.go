package assurance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

type payinControlReader interface {
	GetIntakeControl(context.Context, *payinv1.GetIntakeControlRequest, ...grpc.CallOption) (*payinv1.GetIntakeControlResponse, error)
	ApplyIntakeControl(context.Context, *payinv1.ApplyIntakeControlRequest, ...grpc.CallOption) (*payinv1.ApplyIntakeControlResponse, error)
}
type payoutControlReader interface {
	GetIntakeControl(context.Context, *payoutv1.GetIntakeControlRequest, ...grpc.CallOption) (*payoutv1.GetIntakeControlResponse, error)
	ApplyIntakeControl(context.Context, *payoutv1.ApplyIntakeControlRequest, ...grpc.CallOption) (*payoutv1.ApplyIntakeControlResponse, error)
}

func (m *Module) AdminRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/assurance/summary", m.summaryHandler)
	mux.HandleFunc("GET /admin/assurance/findings", m.findingsHandler)
	mux.HandleFunc("GET /admin/assurance/runs", m.runsHandler)
	mux.HandleFunc("POST /admin/assurance/runs", m.runHandler)
	mux.HandleFunc("POST /admin/assurance/findings/{id}/acknowledge", m.acknowledgeHandler)
	mux.HandleFunc("POST /admin/assurance/findings/{id}/resolve", m.resolveHandler)
	mux.HandleFunc("GET /admin/assurance/intake", m.intakeHandler)
	mux.HandleFunc("POST /admin/assurance/intake/{flow}/pause", m.pauseHandler)
	mux.HandleFunc("POST /admin/assurance/intake/{flow}/resume-requests", m.resumeRequestHandler)
	mux.HandleFunc("POST /admin/assurance/intake/{flow}/resume-requests/{id}/approve", m.resumeApproveHandler)
	return mux
}

func (m *Module) authorized(r *http.Request, roles ...string) bool {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		return false
	}
	for _, role := range roles {
		if claims.Role == role {
			return true
		}
	}
	return false
}

func actorFromRequest(r *http.Request) string {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		return ""
	}
	if claims.UserID != "" {
		return claims.UserID
	}
	return claims.Email
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func (m *Module) summaryHandler(w http.ResponseWriter, r *http.Request) {
	if !m.authorized(r, "admin", "admin_maker", "admin_checker") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	rows, err := m.db.QueryContext(r.Context(), `SELECT severity, status, COUNT(*), COALESCE(SUM(amount_minor),0) FROM assurance_findings GROUP BY severity, status ORDER BY severity, status`)
	if err != nil {
		http.Error(w, "assurance unavailable", http.StatusServiceUnavailable)
		return
	}
	defer rows.Close()
	type item struct {
		Severity string `json:"severity"`
		Status   string `json:"status"`
		Count    int64  `json:"count"`
		Amount   int64  `json:"amount_minor"`
	}
	items := []item{}
	for rows.Next() {
		var value item
		if err := rows.Scan(&value.Severity, &value.Status, &value.Count, &value.Amount); err != nil {
			http.Error(w, "assurance unavailable", http.StatusInternalServerError)
			return
		}
		items = append(items, value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"findings": items, "generated_at": time.Now().UTC()})
}

func (m *Module) findingsHandler(w http.ResponseWriter, r *http.Request) {
	if !m.authorized(r, "admin", "admin_maker", "admin_checker") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 200 {
			http.Error(w, "limit must be between 1 and 200", http.StatusBadRequest)
			return
		}
		limit = value
	}
	where := []string{"1=1"}
	args := []any{}
	for _, field := range []string{"status", "severity", "rule_code", "currency"} {
		if value := r.URL.Query().Get(field); value != "" {
			args = append(args, value)
			where = append(where, fmt.Sprintf("%s=$%d", field, len(args)))
		}
	}
	args = append(args, limit)
	query := `SELECT id, fingerprint, severity, rule_code, resource_id, amount_minor, currency, evidence, first_seen_at, last_seen_at, occurrence_count, status FROM assurance_findings WHERE ` + strings.Join(where, " AND ") + fmt.Sprintf(" ORDER BY last_seen_at DESC, id DESC LIMIT $%d", len(args))
	rows, err := m.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "assurance unavailable", http.StatusServiceUnavailable)
		return
	}
	defer rows.Close()
	findings := []map[string]any{}
	for rows.Next() {
		var id, fingerprint, severity, rule, resource, currency, statusValue string
		var amount, occurrences int64
		var evidence []byte
		var firstSeen, lastSeen time.Time
		if err := rows.Scan(&id, &fingerprint, &severity, &rule, &resource, &amount, &currency, &evidence, &firstSeen, &lastSeen, &occurrences, &statusValue); err != nil {
			http.Error(w, "assurance unavailable", http.StatusInternalServerError)
			return
		}
		var evidenceValue any
		_ = json.Unmarshal(evidence, &evidenceValue)
		findings = append(findings, map[string]any{"id": id, "fingerprint": fingerprint, "severity": severity, "rule_code": rule, "resource_id": resource, "amount_minor": amount, "currency": currency, "evidence": evidenceValue, "first_seen_at": firstSeen, "last_seen_at": lastSeen, "occurrence_count": occurrences, "status": statusValue})
	}
	writeJSON(w, http.StatusOK, map[string]any{"findings": findings})
}

func (m *Module) runsHandler(w http.ResponseWriter, r *http.Request) {
	if !m.authorized(r, "admin", "admin_maker", "admin_checker") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	rows, err := m.db.QueryContext(r.Context(), `SELECT id, mode, status, baseline, cutoff_at, started_at, finished_at, records_scanned, pages_scanned, findings_opened, error_code FROM assurance_runs ORDER BY started_at DESC, id DESC LIMIT 200`)
	if err != nil {
		http.Error(w, "assurance unavailable", http.StatusServiceUnavailable)
		return
	}
	defer rows.Close()
	runs := []map[string]any{}
	for rows.Next() {
		var id, mode, statusValue, errorCode string
		var baseline bool
		var cutoff sql.NullTime
		var started time.Time
		var finished sql.NullTime
		var scanned, pages, opened int
		if err := rows.Scan(&id, &mode, &statusValue, &baseline, &cutoff, &started, &finished, &scanned, &pages, &opened, &errorCode); err != nil {
			http.Error(w, "assurance unavailable", http.StatusInternalServerError)
			return
		}
		run := map[string]any{"id": id, "mode": mode, "status": statusValue, "baseline": baseline, "started_at": started, "records_scanned": scanned, "pages_scanned": pages, "findings_opened": opened, "error_code": errorCode}
		if cutoff.Valid {
			run["cutoff_at"] = cutoff.Time
		}
		if finished.Valid {
			run["finished_at"] = finished.Time
		}
		runs = append(runs, run)
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (m *Module) runHandler(w http.ResponseWriter, r *http.Request) {
	if !m.authorized(r, "admin", "admin_maker", "admin_checker") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	go func() {
		if _, err := m.Run(context.Background(), "manual"); err != nil {
			m.logger.Error("manual assurance run failed", "error", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

func (m *Module) findingMutation(w http.ResponseWriter, r *http.Request, resolved bool) {
	if !m.authorized(r, "admin", "admin_maker", "admin_checker") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid finding id", http.StatusBadRequest)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || strings.TrimSpace(body.Reason) == "" {
		http.Error(w, "reason is required", http.StatusBadRequest)
		return
	}
	statusValue := "acknowledged"
	field := "acknowledged_by"
	if resolved {
		statusValue, field = "resolved", "resolved_by"
	}
	query := `UPDATE assurance_findings SET status=$2, ` + field + `=$3, ` + map[bool]string{true: "resolved_at", false: "acknowledged_at"}[resolved] + `=now() WHERE id=$1`
	result, err := m.db.ExecContext(r.Context(), query, id, statusValue, actorFromRequest(r))
	if err != nil {
		http.Error(w, "assurance unavailable", http.StatusServiceUnavailable)
		return
	}
	if count, _ := result.RowsAffected(); count == 0 {
		http.Error(w, "finding not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": statusValue})
}

func (m *Module) acknowledgeHandler(w http.ResponseWriter, r *http.Request) {
	m.findingMutation(w, r, false)
}
func (m *Module) resolveHandler(w http.ResponseWriter, r *http.Request) {
	m.findingMutation(w, r, true)
}

type intakeCommandRequest struct {
	CommandID        string `json:"command_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

func (m *Module) intakeHandler(w http.ResponseWriter, r *http.Request) {
	if !m.authorized(r, "admin", "admin_maker", "admin_checker") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	result := map[string]any{}
	dependencyErr := false
	if reader, ok := m.payin.(payinControlReader); ok {
		if control, err := reader.GetIntakeControl(r.Context(), &payinv1.GetIntakeControlRequest{}); err == nil {
			result["payin"] = control
		} else {
			dependencyErr = true
		}
	} else {
		dependencyErr = true
	}
	if reader, ok := m.payout.(payoutControlReader); ok {
		if control, err := reader.GetIntakeControl(r.Context(), &payoutv1.GetIntakeControlRequest{}); err == nil {
			result["payout"] = control
		} else {
			dependencyErr = true
		}
	} else {
		dependencyErr = true
	}
	if dependencyErr {
		http.Error(w, "owner unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (m *Module) pauseHandler(w http.ResponseWriter, r *http.Request) {
	if !m.authorized(r, "admin", "admin_maker") {
		http.Error(w, "pause requires admin or admin_maker", http.StatusForbidden)
		return
	}
	m.applyOwnerCommand(w, r, "pause")
}

func (m *Module) applyOwnerCommand(w http.ResponseWriter, r *http.Request, action string) {
	flow := r.PathValue("flow")
	if flow != "payin" && flow != "payout" {
		http.Error(w, "flow must be payin or payout", http.StatusBadRequest)
		return
	}
	var request intakeCommandRequest
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.Reason == "" {
		http.Error(w, "command_id and reason are required", http.StatusBadRequest)
		return
	}
	commandID, err := uuid.Parse(request.CommandID)
	if err != nil {
		http.Error(w, "command_id must be UUID", http.StatusBadRequest)
		return
	}
	actor := actorFromRequest(r)
	insertResult, err := m.db.ExecContext(r.Context(), `INSERT INTO intake_control_commands (id, flow, action, revision, requested_by, reason, status, idempotency_key) VALUES ($1,$2,$3,$4,$5,$6,'pending',$1) ON CONFLICT (idempotency_key) DO NOTHING`, commandID, flow, action, request.ExpectedRevision, actor, request.Reason)
	if err != nil {
		http.Error(w, "command already exists or assurance unavailable", http.StatusConflict)
		return
	}
	if affected, _ := insertResult.RowsAffected(); affected == 0 {
		var statusValue string
		if err := m.db.QueryRowContext(r.Context(), `SELECT status FROM intake_control_commands WHERE id=$1`, commandID).Scan(&statusValue); err == nil && statusValue == "applied" {
			writeJSON(w, http.StatusOK, map[string]any{"id": commandID, "status": statusValue})
			return
		}
		_, _ = m.db.ExecContext(r.Context(), `UPDATE intake_control_commands SET status='pending', error_code='', error_message='' WHERE id=$1`, commandID)
	}
	response, err := m.sendOwnerCommand(r.Context(), flow, action, commandID, request.ExpectedRevision, actor, request.Reason)
	if err != nil {
		_, _ = m.db.ExecContext(r.Context(), `UPDATE intake_control_commands SET status='failed', error_code='OWNER_UNAVAILABLE', error_message=$2 WHERE id=$1`, commandID, err.Error())
		http.Error(w, "owner unavailable", http.StatusServiceUnavailable)
		return
	}
	_, _ = m.db.ExecContext(r.Context(), `UPDATE intake_control_commands SET status='applied', approved_by=$2, resulting_revision=$3, applied_at=now() WHERE id=$1`, commandID, actor, responseRevision(response))
	writeJSON(w, http.StatusOK, response)
}

func (m *Module) sendOwnerCommand(ctx context.Context, flow, action string, commandID uuid.UUID, revision int64, actor, reason string) (any, error) {
	var response any
	var err error
	if flow == "payin" {
		reader, ok := m.payin.(payinControlReader)
		if !ok {
			return nil, errors.New("payin unavailable")
		}
		response, err = reader.ApplyIntakeControl(ctx, &payinv1.ApplyIntakeControlRequest{CommandId: commandID.String(), Action: action, ExpectedRevision: revision, Actor: actor, Reason: reason})
	} else {
		reader, ok := m.payout.(payoutControlReader)
		if !ok {
			return nil, errors.New("payout unavailable")
		}
		response, err = reader.ApplyIntakeControl(ctx, &payoutv1.ApplyIntakeControlRequest{CommandId: commandID.String(), Action: action, ExpectedRevision: revision, Actor: actor, Reason: reason})
	}
	return response, err
}

func (m *Module) resumeRequestHandler(w http.ResponseWriter, r *http.Request) {
	if !m.authorized(r, "admin", "admin_maker") {
		http.Error(w, "resume request requires admin or admin_maker", http.StatusForbidden)
		return
	}
	flow := r.PathValue("flow")
	if flow != "payin" && flow != "payout" {
		http.Error(w, "flow must be payin or payout", http.StatusBadRequest)
		return
	}
	var request intakeCommandRequest
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.Reason == "" {
		http.Error(w, "command_id and reason are required", http.StatusBadRequest)
		return
	}
	commandID, err := uuid.Parse(request.CommandID)
	if err != nil {
		http.Error(w, "command_id must be UUID", http.StatusBadRequest)
		return
	}
	insertResult, err := m.db.ExecContext(r.Context(), `INSERT INTO intake_control_commands (id, flow, action, revision, requested_by, reason, status, idempotency_key) VALUES ($1,$2,'resume_request',$3,$4,$5,'pending',$1) ON CONFLICT (idempotency_key) DO NOTHING`, commandID, flow, request.ExpectedRevision, actorFromRequest(r), request.Reason)
	if err != nil {
		http.Error(w, "command already exists or assurance unavailable", http.StatusConflict)
		return
	}
	if affected, _ := insertResult.RowsAffected(); affected == 0 {
		var statusValue string
		if err := m.db.QueryRowContext(r.Context(), `SELECT status FROM intake_control_commands WHERE id=$1`, commandID).Scan(&statusValue); err == nil {
			writeJSON(w, http.StatusAccepted, map[string]any{"id": commandID, "status": statusValue})
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": commandID, "status": "pending"})
}

func (m *Module) resumeApproveHandler(w http.ResponseWriter, r *http.Request) {
	if !m.authorized(r, "admin", "admin_checker") {
		http.Error(w, "approval requires admin or admin_checker", http.StatusForbidden)
		return
	}
	commandID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid command id", http.StatusBadRequest)
		return
	}
	flow := r.PathValue("flow")
	var requestedBy string
	var revision int64
	if err := m.db.QueryRowContext(r.Context(), `SELECT requested_by, revision FROM intake_control_commands WHERE id=$1 AND flow=$2 AND action='resume_request' AND status IN ('pending','failed')`, commandID, flow).Scan(&requestedBy, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "resume request not found or already approved", http.StatusNotFound)
			return
		}
		http.Error(w, "assurance unavailable", http.StatusServiceUnavailable)
		return
	}
	actor := actorFromRequest(r)
	if actor == requestedBy {
		http.Error(w, "requester and approver must differ", http.StatusForbidden)
		return
	}
	approvalResult, err := m.db.ExecContext(r.Context(), `UPDATE intake_control_commands SET status='applying', approved_by=$2, error_code='', error_message='' WHERE id=$1 AND status IN ('pending','failed')`, commandID, actor)
	if err != nil {
		http.Error(w, "assurance unavailable", http.StatusServiceUnavailable)
		return
	}
	if affected, _ := approvalResult.RowsAffected(); affected == 0 {
		http.Error(w, "resume request already being processed", http.StatusConflict)
		return
	}
	response, err := m.sendOwnerCommand(r.Context(), flow, "resume", commandID, revision, actor, "approved by "+actor)
	if err != nil {
		_, _ = m.db.ExecContext(r.Context(), `UPDATE intake_control_commands SET status='failed', error_code='OWNER_UNAVAILABLE', error_message=$2 WHERE id=$1`, commandID, err.Error())
		http.Error(w, "owner unavailable", http.StatusServiceUnavailable)
		return
	}
	_, _ = m.db.ExecContext(r.Context(), `UPDATE intake_control_commands SET action='resume_approve', status='applied', resulting_revision=$2, applied_at=now() WHERE id=$1`, commandID, responseRevision(response))
	writeJSON(w, http.StatusOK, response)
}

func responseRevision(value any) int64 {
	switch result := value.(type) {
	case *payinv1.ApplyIntakeControlResponse:
		return result.GetRevision()
	case *payoutv1.ApplyIntakeControlResponse:
		return result.GetRevision()
	default:
		return 0
	}
}
