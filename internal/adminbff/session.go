package adminbff

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/pkg/database"
)

var ErrSessionNotFound = errors.New("adminbff: session not found")

type Session struct {
	ID                string
	UserID            uuid.UUID
	Email             string
	Role              string
	CSRFToken         string
	CreatedAt         time.Time
	LastSeenAt        time.Time
	ExpiresAt         time.Time
	AbsoluteExpiresAt time.Time
}

type SessionRepository interface {
	CreateSession(context.Context, Session) error
	GetSession(context.Context, string) (Session, error)
	TouchSession(context.Context, string, time.Time) error
	DeleteSession(context.Context, string) error
	CleanupSessions(context.Context, time.Time) error
}

type sessionRepo struct{ db database.DatabaseSQL }

func NewSessionRepository(db database.DatabaseSQL) SessionRepository { return &sessionRepo{db: db} }

func NewOpaqueToken(size int) (string, error) {
	if size <= 0 {
		size = 32
	}
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("adminbff: generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func (r *sessionRepo) CreateSession(ctx context.Context, s Session) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sessions
			(id, user_id, email, role, csrf_token, created_at, last_seen_at, expires_at, absolute_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		s.ID, s.UserID, s.Email, s.Role, s.CSRFToken, s.CreatedAt, s.LastSeenAt, s.ExpiresAt, s.AbsoluteExpiresAt)
	if err != nil {
		return fmt.Errorf("adminbff: create session: %w", err)
	}
	return nil
}

func (r *sessionRepo) GetSession(ctx context.Context, id string) (Session, error) {
	var s Session
	err := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, email, role, csrf_token, created_at, last_seen_at, expires_at, absolute_expires_at
		FROM sessions
		WHERE id = $1 AND expires_at > now() AND absolute_expires_at > now()`, id).
		Scan(&s.ID, &s.UserID, &s.Email, &s.Role, &s.CSRFToken, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt, &s.AbsoluteExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("adminbff: get session: %w", err)
	}
	return s, nil
}

func (r *sessionRepo) TouchSession(ctx context.Context, id string, expiresAt time.Time) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE sessions SET last_seen_at = now(), expires_at = $1
		WHERE id = $2 AND expires_at > now() AND absolute_expires_at > now()`, expiresAt, id)
	if err != nil {
		return fmt.Errorf("adminbff: touch session: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("adminbff: touch session rows: %w", err)
	}
	if changed == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (r *sessionRepo) DeleteSession(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = $1`, id); err != nil {
		return fmt.Errorf("adminbff: delete session: %w", err)
	}
	return nil
}

func (r *sessionRepo) CleanupSessions(ctx context.Context, now time.Time) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= $1 OR absolute_expires_at <= $1`, now); err != nil {
		return fmt.Errorf("adminbff: cleanup sessions: %w", err)
	}
	return nil
}
