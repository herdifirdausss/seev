package repository

//go:generate mockgen -source=repository.go -destination=repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalerror"
)

// ErrNotFound is returned when no row matches the lookup.
var ErrNotFound = errors.New("auth: not found")

// ErrDuplicateEmail is returned by CreateUser when the (case-insensitive)
// email is already registered — backed by idx_auth_users_email, never a
// read-then-write race.
var ErrDuplicateEmail = errors.New("auth: email already registered")
var ErrKYCSubmissionNotFound = errors.New("auth: kyc submission not found")
var ErrKYCSubmissionNotPending = errors.New("auth: kyc submission is not pending")
var ErrKYCTierConflict = errors.New("auth: kyc tier conflict")

// Repository persists auth identities, credentials, and refresh tokens
// (docs/plan/25 Task T1).
type Repository interface {
	// CreateUser inserts the identity + credential rows in one transaction.
	// Returns ErrDuplicateEmail on a case-insensitive email collision.
	CreateUser(ctx context.Context, u model.User, passwordHash string) error
	// GetUserByEmail looks up by lower(email). ErrNotFound when absent.
	GetUserByEmail(ctx context.Context, email string) (model.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (model.User, error)
	// GetPasswordHash returns the bcrypt hash for the user — the ONLY read
	// path for auth_credentials, used solely inside Module.Login.
	GetPasswordHash(ctx context.Context, userID uuid.UUID) (string, error)
	UpdateFullName(ctx context.Context, userID uuid.UUID, fullName string) error

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

	CreateKYCSubmission(ctx context.Context, s model.KYCSubmission) error
	GetLatestKYCSubmission(ctx context.Context, userID uuid.UUID) (model.KYCSubmission, error)
	GetKYCSubmission(ctx context.Context, id uuid.UUID) (model.KYCSubmission, error)
	ListKYCSubmissions(ctx context.Context, status string) ([]model.KYCSubmission, error)
	// ApproveKYCSubmission runs applyTier while the pending row is locked and
	// commits the auth level/submission decision only after it succeeds.
	ApproveKYCSubmission(ctx context.Context, id uuid.UUID, decidedBy, providerRef, reason string, applyTier func(context.Context, uuid.UUID, int) error) error
	RejectKYCSubmission(ctx context.Context, id uuid.UUID, decidedBy, reason string) error
}

type repo struct {
	db database.DatabaseSQL
}

func NewRepository(db database.DatabaseSQL) Repository {
	return &repo{db: db}
}

func (r *repo) CreateUser(ctx context.Context, u model.User, passwordHash string) error {
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO auth_users (id, email, full_name, role, status, kyc_level)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			u.ID, u.Email, u.FullName, u.Role, u.Status, u.KYCLevel); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO auth_credentials (user_id, password_hash)
			VALUES ($1, $2)`,
			u.ID, passwordHash); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if generalerror.IsDuplicateKey(err) {
			return ErrDuplicateEmail
		}
		return fmt.Errorf("auth: create user: %w", err)
	}
	return nil
}

const userColumns = `id, email, full_name, role, status, kyc_level, created_at, updated_at`

func scanUser(row *sql.Row) (model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.Status, &u.KYCLevel, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	if err != nil {
		return model.User{}, fmt.Errorf("auth: scan user: %w", err)
	}
	return u, nil
}

func (r *repo) GetUserByEmail(ctx context.Context, email string) (model.User, error) {
	return scanUser(r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM auth_users WHERE lower(email) = lower($1)`, email))
}

func (r *repo) GetUserByID(ctx context.Context, id uuid.UUID) (model.User, error) {
	return scanUser(r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM auth_users WHERE id = $1`, id))
}

func (r *repo) GetPasswordHash(ctx context.Context, userID uuid.UUID) (string, error) {
	var hash string
	err := r.db.QueryRowContext(ctx,
		`SELECT password_hash FROM auth_credentials WHERE user_id = $1`, userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("auth: get password hash: %w", err)
	}
	return hash, nil
}

func (r *repo) UpdateFullName(ctx context.Context, userID uuid.UUID, fullName string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE auth_users SET full_name = $1, updated_at = now() WHERE id = $2`, fullName, userID)
	if err != nil {
		return fmt.Errorf("auth: update full name: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth: update full name: rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *repo) InsertRefreshToken(ctx context.Context, t model.RefreshToken) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO auth_refresh_tokens (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)`,
		t.ID, t.UserID, t.TokenHash, t.ExpiresAt)
	if err != nil {
		return fmt.Errorf("auth: insert refresh token: %w", err)
	}
	return nil
}

func (r *repo) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (model.RefreshToken, error) {
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

func (r *repo) RevokeRefreshToken(ctx context.Context, id uuid.UUID, replacedBy *uuid.UUID) (bool, error) {
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

func (r *repo) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE auth_refresh_tokens SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > $2`, userID, time.Now())
	if err != nil {
		return fmt.Errorf("auth: revoke all for user: %w", err)
	}
	return nil
}

const kycSubmissionColumns = `id, user_id, level_requested, status, payload, provider, provider_ref, decided_by, decision_reason, created_at, decided_at`

func scanKYCSubmission(scanner interface{ Scan(...any) error }) (model.KYCSubmission, error) {
	var s model.KYCSubmission
	var payload []byte
	var providerRef, decidedBy, reason sql.NullString
	var decidedAt sql.NullTime
	if err := scanner.Scan(&s.ID, &s.UserID, &s.LevelRequested, &s.Status, &payload, &s.Provider, &providerRef, &decidedBy, &reason, &s.CreatedAt, &decidedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.KYCSubmission{}, ErrKYCSubmissionNotFound
		}
		return model.KYCSubmission{}, fmt.Errorf("auth: scan kyc submission: %w", err)
	}
	if err := json.Unmarshal(payload, &s.Payload); err != nil {
		return model.KYCSubmission{}, fmt.Errorf("auth: decode kyc payload: %w", err)
	}
	if providerRef.Valid {
		s.ProviderRef = providerRef.String
	}
	if decidedBy.Valid {
		s.DecidedBy = decidedBy.String
	}
	if reason.Valid {
		s.DecisionReason = reason.String
	}
	if decidedAt.Valid {
		value := decidedAt.Time
		s.DecidedAt = &value
	}
	return s, nil
}

func (r *repo) CreateKYCSubmission(ctx context.Context, s model.KYCSubmission) error {
	payload, err := s.MarshalPayload()
	if err != nil {
		return fmt.Errorf("auth: encode kyc payload: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO kyc_submissions (id, user_id, level_requested, status, payload, provider)
		VALUES ($1, $2, $3, 'pending', $4, $5)`, s.ID, s.UserID, s.LevelRequested, payload, s.Provider)
	if err != nil {
		if generalerror.IsDuplicateKey(err) {
			return ErrKYCSubmissionNotPending
		}
		return fmt.Errorf("auth: create kyc submission: %w", err)
	}
	return nil
}

func (r *repo) GetLatestKYCSubmission(ctx context.Context, userID uuid.UUID) (model.KYCSubmission, error) {
	return scanKYCSubmission(r.db.QueryRowContext(ctx, `SELECT `+kycSubmissionColumns+` FROM kyc_submissions WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, userID))
}

func (r *repo) GetKYCSubmission(ctx context.Context, id uuid.UUID) (model.KYCSubmission, error) {
	return scanKYCSubmission(r.db.QueryRowContext(ctx, `SELECT `+kycSubmissionColumns+` FROM kyc_submissions WHERE id = $1`, id))
}

func (r *repo) ListKYCSubmissions(ctx context.Context, status string) ([]model.KYCSubmission, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+kycSubmissionColumns+` FROM kyc_submissions WHERE ($1 = '' OR status = $1) ORDER BY created_at ASC, id ASC`, status)
	if err != nil {
		return nil, fmt.Errorf("auth: list kyc submissions: %w", err)
	}
	defer rows.Close()
	var out []model.KYCSubmission
	for rows.Next() {
		s, scanErr := scanKYCSubmission(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: iterate kyc submissions: %w", err)
	}
	return out, nil
}

func (r *repo) ApproveKYCSubmission(ctx context.Context, id uuid.UUID, decidedBy, providerRef, reason string, applyTier func(context.Context, uuid.UUID, int) error) error {
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var userID uuid.UUID
		var level int
		var status string
		err := tx.QueryRowContext(ctx, `SELECT user_id, level_requested, status FROM kyc_submissions WHERE id = $1 FOR UPDATE`, id).Scan(&userID, &level, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrKYCSubmissionNotFound
		}
		if err != nil {
			return fmt.Errorf("auth: lock kyc submission: %w", err)
		}
		if status != "pending" {
			return ErrKYCSubmissionNotPending
		}
		if err := applyTier(ctx, userID, level); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE auth_users SET kyc_level = $1, updated_at = now() WHERE id = $2 AND kyc_level + 1 = $1`, level, userID)
		if err != nil {
			return fmt.Errorf("auth: update kyc level: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("auth: update kyc level rows: %w", err)
		}
		if changed != 1 {
			return ErrKYCTierConflict
		}
		result, err = tx.ExecContext(ctx, `UPDATE kyc_submissions SET status = 'approved', provider_ref = NULLIF($1, ''), decision_reason = NULLIF($2, ''), decided_by = $3, decided_at = now() WHERE id = $4 AND status = 'pending'`, providerRef, reason, decidedBy, id)
		if err != nil {
			return fmt.Errorf("auth: approve kyc submission: %w", err)
		}
		if changed, err = result.RowsAffected(); err != nil {
			return fmt.Errorf("auth: approve kyc rows: %w", err)
		} else if changed != 1 {
			return ErrKYCSubmissionNotPending
		}
		return nil
	})
}

func (r *repo) RejectKYCSubmission(ctx context.Context, id uuid.UUID, decidedBy, reason string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE kyc_submissions SET status = 'rejected', decided_by = $1, decision_reason = $2, decided_at = now() WHERE id = $3 AND status = 'pending'`, decidedBy, reason, id)
	if err != nil {
		return fmt.Errorf("auth: reject kyc submission: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth: reject kyc rows: %w", err)
	}
	if changed == 0 {
		return ErrKYCSubmissionNotPending
	}
	return nil
}
