package repository

//go:generate mockgen -source=repository.go -destination=repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/notify/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

// ErrNotFound is returned by Get when no row exists for the given id.
var ErrNotFound = errors.New("notify: notification not found")

// Repository persists in-app notifications (docs/roadmap/archive/25 Task T4).
type Repository interface {
	// Insert dedups via the UNIQUE(event_id, user_id) constraint —
	// `INSERT ... ON CONFLICT (event_id, user_id) DO NOTHING`. inserted=false
	// means a row for this (n.EventID, n.UserID) pair already existed (a
	// RabbitMQ redelivery of an already-processed event) — the consumer
	// treats this identically to a fresh insert: ack, not an error.
	Insert(ctx context.Context, n model.Notification) (inserted bool, err error)

	Get(ctx context.Context, id uuid.UUID) (model.Notification, error)

	// List returns userID's own notifications, newest first, keyset-paginated
	// on created_at. before.IsZero() means "start from the most recent".
	List(ctx context.Context, userID uuid.UUID, limit int, before time.Time) ([]model.Notification, error)

	// MarkRead is a conditional UPDATE (WHERE id = $1 AND user_id = $2) —
	// ownership is enforced at the SQL layer, not only the HTTP layer.
	// matched=false covers both "no such id" and "not this user's row"; the
	// HTTP layer maps both to 404, never confirming existence to a
	// non-owner (docs/roadmap/archive/23's GetHandler ownership pattern).
	MarkRead(ctx context.Context, id, userID uuid.UUID) (matched bool, err error)
}

type repo struct {
	db database.DatabaseSQL
}

func NewRepository(db database.DatabaseSQL) Repository {
	return &repo{db: db}
}

func (r *repo) Insert(ctx context.Context, n model.Notification) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO notif_notifications (id, user_id, event_id, type, title, body, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (event_id, user_id) DO NOTHING`,
		n.ID, n.UserID, n.EventID, n.Type, n.Title, n.Body, n.Payload,
	)
	if err != nil {
		return false, fmt.Errorf("insert notification: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert notification rows affected: %w", err)
	}
	return affected > 0, nil
}

func (r *repo) Get(ctx context.Context, id uuid.UUID) (model.Notification, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, event_id, type, title, body, payload, read_at, created_at
		FROM notif_notifications WHERE id = $1`, id)
	n, err := scanNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Notification{}, ErrNotFound
	}
	return n, err
}

func (r *repo) List(ctx context.Context, userID uuid.UUID, limit int, before time.Time) ([]model.Notification, error) {
	var rows *sql.Rows
	var err error
	if before.IsZero() {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, user_id, event_id, type, title, body, payload, read_at, created_at
			FROM notif_notifications WHERE user_id = $1
			ORDER BY created_at DESC LIMIT $2`, userID, limit)
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, user_id, event_id, type, title, body, payload, read_at, created_at
			FROM notif_notifications WHERE user_id = $1 AND created_at < $2
			ORDER BY created_at DESC LIMIT $3`, userID, before, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	var out []model.Notification
	for rows.Next() {
		n, err := scanNotificationRows(rows)
		if err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notifications: %w", err)
	}
	return out, nil
}

func (r *repo) MarkRead(ctx context.Context, id, userID uuid.UUID) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE notif_notifications SET read_at = now()
		WHERE id = $1 AND user_id = $2 AND read_at IS NULL`, id, userID)
	if err != nil {
		return false, fmt.Errorf("mark notification read: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mark notification read rows affected: %w", err)
	}
	if affected > 0 {
		return true, nil
	}
	// affected=0 is ambiguous (not found / not owned / already read) —
	// disambiguate "already read by its owner" from "not found or not
	// owned" so MarkRead is idempotent for its own owner instead of
	// reporting a confusing 404 on a second call.
	var exists bool
	err = r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM notif_notifications WHERE id = $1 AND user_id = $2)`, id, userID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check notification ownership: %w", err)
	}
	return exists, nil
}

// rowScanner abstracts *sql.Row / *sql.Rows so both callers share one scan
// implementation.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanNotification(row *sql.Row) (model.Notification, error) {
	return scan(row)
}

func scanNotificationRows(rows *sql.Rows) (model.Notification, error) {
	return scan(rows)
}

func scan(s rowScanner) (model.Notification, error) {
	var n model.Notification
	var readAt sql.NullTime
	if err := s.Scan(&n.ID, &n.UserID, &n.EventID, &n.Type, &n.Title, &n.Body, &n.Payload, &readAt, &n.CreatedAt); err != nil {
		return model.Notification{}, err
	}
	if readAt.Valid {
		n.ReadAt = &readAt.Time
	}
	return n, nil
}
