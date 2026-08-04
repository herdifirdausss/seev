package auth

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// AdminPrivacyRequest is docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T6's own
// "admin BFF status panels without exposing subject data" — deliberately
// carries UserID (an opaque reference an operator needs to act on a stuck
// request, e.g. to cross-check a hold or KYC submission) but NEVER email,
// full_name, or the active-saga ciphertext, none of which this table
// stores in the first place except the ciphertext (never selected here).
type AdminPrivacyRequest struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	RequestType  string
	Status       string
	RequestedAt  time.Time
	ReadyAt      *time.Time
	ErrorMessage string
	RetryCount   int
}

// AdminListPrivacyRequests lists export/closure requests newest first,
// optionally filtered by request_type ("export"|"closure") and/or status
// — the read path behind the admin BFF's privacy status panel.
func (m *Module) AdminListPrivacyRequests(ctx context.Context, requestType, status string, limit int) ([]AdminPrivacyRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, user_id, request_type, status, requested_at, ready_at, COALESCE(error_message, ''), retry_count
		FROM privacy_requests
		WHERE ($1 = '' OR request_type = $1) AND ($2 = '' OR status = $2)
		ORDER BY requested_at DESC
		LIMIT $3`, requestType, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AdminPrivacyRequest
	for rows.Next() {
		var req AdminPrivacyRequest
		var readyAt sql.NullTime
		if err := rows.Scan(&req.ID, &req.UserID, &req.RequestType, &req.Status, &req.RequestedAt, &readyAt, &req.ErrorMessage, &req.RetryCount); err != nil {
			return nil, err
		}
		if readyAt.Valid {
			t := readyAt.Time
			req.ReadyAt = &t
		}
		out = append(out, req)
	}
	return out, rows.Err()
}
