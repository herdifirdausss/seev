package adminbff

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/herdifirdausss/seev/pkg/database"
)

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
}

type auditWriter interface {
	WriteAudit(context.Context, AuditEntry) error
}

type auditReader interface {
	ListAudit(context.Context, int) ([]AuditEntry, error)
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

func (r *auditRepo) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT user_id, email, role, method, route_pattern, target_service,
		       resource_id, outcome, request_id, summary
		FROM audit_log ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("adminbff: list audit: %w", err)
	}
	defer rows.Close()
	entries := make([]AuditEntry, 0, limit)
	for rows.Next() {
		var entry AuditEntry
		var userID string
		var summary []byte
		if err := rows.Scan(&userID, &entry.Email, &entry.Role, &entry.Method,
			&entry.RoutePattern, &entry.TargetService, &entry.ResourceID,
			&entry.Outcome, &entry.RequestID, &summary); err != nil {
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
