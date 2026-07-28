// Package notify's own owner-side of docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T4b/T5b
// (K9, K10, K11) — this is K11's own "Gateway" row (gateway has no
// internal/gateway package of its own; notify is the module that owns
// notif_notifications, the table K11 means). Mirrors internal/payin's
// own privacy.go shape.
package notify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/pkg/privacyexport"
)

// PrivacyPrepareClosure: notifications are a read-log of already-posted
// events — nothing "in flight" to strand. Gateway/notify has no K10
// blocking condition of its own.
func (m *Module) PrivacyPrepareClosure(ctx context.Context, subjectID uuid.UUID) (blocked bool, reasons []string, err error) {
	return false, nil, nil
}

// PrivacyCommitClosure repoints notif_notifications.user_id to the
// surrogate — K11's own "notification user references or deletion by
// policy" (repoint chosen over deletion for consistency with every other
// owner's Commit semantics; retention's own existing purge classes still
// delete on their own read/age schedule regardless). Idempotent without
// its own checkpoint, same re-derive-from-current-state convention as
// every other owner.
func (m *Module) PrivacyCommitClosure(ctx context.Context, subjectID, surrogateID uuid.UUID) (resultHash string, affectedCount int, err error) {
	if _, err := m.db.ExecContext(ctx, `UPDATE notif_notifications SET user_id = $1 WHERE user_id = $2`, surrogateID, subjectID); err != nil {
		return "", 0, fmt.Errorf("notify closure commit: %w", err)
	}

	rows, err := m.db.QueryContext(ctx, `SELECT id FROM notif_notifications WHERE user_id = $1 ORDER BY id`, surrogateID)
	if err != nil {
		return "", 0, fmt.Errorf("notify closure commit result: %w", err)
	}
	defer rows.Close()

	h := sha256.New()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return "", 0, fmt.Errorf("notify closure commit scan: %w", err)
		}
		h.Write([]byte(id.String()))
		affectedCount++
	}
	if err := rows.Err(); err != nil {
		return "", 0, fmt.Errorf("notify closure commit iterate: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), affectedCount, nil
}

// privacyExportNotificationRow is docs/roadmap/archive/51 T4b's own hand-written
// export DTO. `payload` is excluded — it's an internal rendering aid
// (TransactionPosted-derived), `title`/`body` already carry the
// user-facing content.
type privacyExportNotificationRow struct {
	Type      string     `json:"type"`
	ID        string     `json:"id"`
	NotifType string     `json:"notification_type"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// PrivacyExportRows returns the subject's own notifications as of cutoff.
func (m *Module) PrivacyExportRows(ctx context.Context, subjectID uuid.UUID, cutoff time.Time) ([]json.RawMessage, error) {
	var all []json.RawMessage
	for offset := 0; ; {
		page, next, err := m.PrivacyExportPage(ctx, subjectID, cutoff, offset, privacyexport.DefaultPageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if next == "" {
			return all, nil
		}
		offset += len(page)
	}
}

func (m *Module) PrivacyExportPage(ctx context.Context, subjectID uuid.UUID, cutoff time.Time, offset, pageSize int) ([]json.RawMessage, string, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, type, title, body, read_at, created_at FROM notif_notifications
		WHERE user_id = $1 AND created_at <= $2 ORDER BY created_at, id
		LIMIT $3 OFFSET $4`, subjectID, cutoff, pageSize+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("notify export: notifications: %w", err)
	}
	defer rows.Close()

	var out []json.RawMessage
	for rows.Next() {
		var row privacyExportNotificationRow
		row.Type = "notification"
		var readAt *time.Time
		if err := rows.Scan(&row.ID, &row.NotifType, &row.Title, &row.Body, &readAt, &row.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("notify export: scan notifications: %w", err)
		}
		row.ReadAt = readAt
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, "", fmt.Errorf("notify export: encode notification: %w", err)
		}
		out = append(out, encoded)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("notify export: iterate notifications: %w", err)
	}
	hasMore := len(out) > pageSize
	if hasMore {
		out = out[:pageSize]
	}
	return out, privacyexport.Next(offset, len(out), hasMore), nil
}
