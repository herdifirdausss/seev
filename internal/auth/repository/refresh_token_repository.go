package repository

//go:generate mockgen -source=refresh_token_repository.go -destination=refresh_token_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

// RefreshTokenRepository persists opaque refresh tokens for rotation and
// replay detection.
type RefreshTokenRepository interface {
	InsertRefreshToken(ctx context.Context, t model.RefreshToken) error
	// GetRefreshTokenByHash looks up by token_hash (SHA-256 hex).
	// ErrNotFound when absent.
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (model.RefreshToken, error)
	// RevokeRefreshToken marks one token revoked, recording its successor.
	// Conditional on not already being revoked — returns won=false if
	// another refresh raced us to it (caller treats that as token reuse).
	RevokeRefreshToken(ctx context.Context, id uuid.UUID, replacedBy *uuid.UUID) (bool, error)
	// RevokeAllForUser revokes every live token the user has — the replay
	// containment response when a revoked token is presented again.
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

type refreshTokenRepo struct {
	db database.DatabaseSQL
}

func NewRefreshTokenRepository(db database.DatabaseSQL) RefreshTokenRepository {
	return &refreshTokenRepo{db: db}
}

func (r *refreshTokenRepo) InsertRefreshToken(ctx context.Context, t model.RefreshToken) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO auth_refresh_tokens (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)`,
		t.ID, t.UserID, t.TokenHash, t.ExpiresAt)
	if err != nil {
		return fmt.Errorf("auth: insert refresh token: %w", err)
	}
	return nil
}

func (r *refreshTokenRepo) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (model.RefreshToken, error) {
	var t model.RefreshToken
	var revokedAt sql.NullTime
	var replacedBy sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, token_hash, expires_at, created_at, revoked_at, replaced_by
		FROM auth_refresh_tokens WHERE token_hash = $1`, tokenHash).
		Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.CreatedAt, &revokedAt, &replacedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return model.RefreshToken{}, ErrNotFound
	}
	if err != nil {
		return model.RefreshToken{}, fmt.Errorf("auth: get refresh token: %w", err)
	}
	if revokedAt.Valid {
		v := revokedAt.Time
		t.RevokedAt = &v
	}
	if replacedBy.Valid {
		id, parseErr := uuid.Parse(replacedBy.String)
		if parseErr == nil {
			t.ReplacedBy = &id
		}
	}
	return t, nil
}

func (r *refreshTokenRepo) RevokeRefreshToken(ctx context.Context, id uuid.UUID, replacedBy *uuid.UUID) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE auth_refresh_tokens SET revoked_at = now(), replaced_by = $1
		WHERE id = $2 AND revoked_at IS NULL`, replacedBy, id)
	if err != nil {
		return false, fmt.Errorf("auth: revoke refresh token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("auth: revoke refresh token: rows affected: %w", err)
	}
	return n == 1, nil
}

func (r *refreshTokenRepo) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE auth_refresh_tokens SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > $2`, userID, time.Now())
	if err != nil {
		return fmt.Errorf("auth: revoke all for user: %w", err)
	}
	return nil
}
