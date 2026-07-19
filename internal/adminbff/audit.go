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

type auditRepo struct{ db database.DatabaseSQL }

func newAuditRepository(db database.DatabaseSQL) auditWriter { return &auditRepo{db: db} }

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
