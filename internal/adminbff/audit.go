package adminbff

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/herdifirdausss/seev/pkg/database"
)

var auditWriteFailuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Namespace: "adminbff", Name: "audit_write_failures_total", Help: "Admin BFF audit writes that failed while the mutation continued.",
})

func init() {
	_ = prometheus.Register(auditWriteFailuresTotal)
}

type AuditEntry struct {
	UserID        string
	Email         string
	Role          string
	Method        string
	RoutePattern  string
	TargetService string
	ResourceID    string
	Outcome       int
	RequestID     string
	Summary       map[string]any
	CreatedAt     time.Time
}

type AuditFilter struct {
	Limit    int
	Operator string
	Service  string
	From     *time.Time
	To       *time.Time
}

type auditWriter interface {
	WriteAudit(context.Context, AuditEntry) error
}

type auditReader interface {
	ListAudit(context.Context, AuditFilter) ([]AuditEntry, error)
}

type auditRepo struct{ db database.DatabaseSQL }

func newAuditRepository(db database.DatabaseSQL) *auditRepo { return &auditRepo{db: db} }

func (r *auditRepo) WriteAudit(ctx context.Context, entry AuditEntry) error {
	summary, err := json.Marshal(entry.Summary)
	if err != nil {
		return fmt.Errorf("adminbff: encode audit summary: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO audit_log
			(user_id, email, role, method, route_pattern, target_service, resource_id, outcome, request_id, summary)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)`,
		entry.UserID, entry.Email, entry.Role, entry.Method, entry.RoutePattern,
		entry.TargetService, entry.ResourceID, entry.Outcome, entry.RequestID, summary)
	if err != nil {
		return fmt.Errorf("adminbff: write audit: %w", err)
	}
	return nil
}

func (r *auditRepo) ListAudit(ctx context.Context, filter AuditFilter) ([]AuditEntry, error) {
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT user_id, email, role, method, route_pattern, target_service,
		       resource_id, outcome, request_id, summary, created_at
		FROM audit_log
		WHERE ($1 = '' OR user_id::text = $1 OR email = $1)
		  AND ($2 = '' OR target_service = $2)
		  AND ($3::timestamptz IS NULL OR created_at >= $3)
		  AND ($4::timestamptz IS NULL OR created_at < $4)
		ORDER BY created_at DESC, id DESC LIMIT $5`,
		filter.Operator, filter.Service, filter.From, filter.To, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("adminbff: list audit: %w", err)
	}
	defer rows.Close()
	entries := make([]AuditEntry, 0, filter.Limit)
	for rows.Next() {
		var entry AuditEntry
		var userID string
		var summary []byte
		if err := rows.Scan(&userID, &entry.Email, &entry.Role, &entry.Method,
			&entry.RoutePattern, &entry.TargetService, &entry.ResourceID,
			&entry.Outcome, &entry.RequestID, &summary, &entry.CreatedAt); err != nil {
			return nil, fmt.Errorf("adminbff: scan audit: %w", err)
		}
		entry.UserID = userID
		if len(summary) > 0 {
			if err := json.Unmarshal(summary, &entry.Summary); err != nil {
				return nil, fmt.Errorf("adminbff: decode audit summary: %w", err)
			}
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (m *Module) AuditMutation(ctx context.Context, r *http.Request, target string, outcome int, summary map[string]any) {
	session := SessionFromContext(ctx)
	if session == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
		return
	}
	if err := m.audit.WriteAudit(ctx, AuditEntry{
		UserID: session.UserID.String(), Email: session.Email, Role: session.Role,
		Method: r.Method, RoutePattern: r.Pattern, TargetService: target,
		ResourceID: resourceID(r), Outcome: outcome, RequestID: r.Header.Get("X-Request-Id"), Summary: summary,
	}); err != nil {
		auditWriteFailuresTotal.Inc()
		m.logger.Error("adminbff: audit write failed", "error", err)
	}
}

func resourceID(r *http.Request) string {
	if id := r.PathValue("id"); id != "" {
		return id
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func (m *Module) auditListHandler(w http.ResponseWriter, r *http.Request) {
	filter := AuditFilter{Limit: 100, Operator: r.URL.Query().Get("operator"), Service: r.URL.Query().Get("service")}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if value, err := strconv.Atoi(raw); err != nil || value <= 0 {
			writeJSONError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit must be positive")
			return
		} else if value < filter.Limit {
			filter.Limit = value
		}
	}
	for name, destination := range map[string]**time.Time{"from": &filter.From, "to": &filter.To} {
		if raw := r.URL.Query().Get(name); raw != "" {
			value, parseErr := time.Parse(time.RFC3339, raw)
			if parseErr != nil {
				writeJSONError(w, http.StatusBadRequest, "INVALID_TIME", name+" must be RFC3339")
				return
			}
			*destination = &value
		}
	}
	entries, err := m.auditRead.ListAudit(r.Context(), filter)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "AUDIT_UNAVAILABLE", "audit log unavailable")
		return
	}
	response := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		response = append(response, map[string]any{
			"user_id": entry.UserID, "email": entry.Email, "role": entry.Role,
			"method": entry.Method, "route_pattern": entry.RoutePattern,
			"target_service": entry.TargetService, "resource_id": entry.ResourceID,
			"outcome": entry.Outcome, "request_id": entry.RequestID, "summary": entry.Summary,
			"created_at": entry.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": response})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
