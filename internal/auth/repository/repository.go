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
var ErrKYCApplyTier = errors.New("auth: apply kyc tier failed")

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

	// KYC apply retry intents use a short lease rather than a processing status:
	// a crashed worker leaves the row pending and it becomes claimable again
	// after locked_until.
	EnqueueKYCApplyRetry(ctx context.Context, retry model.KYCApplyRetry) error
	ClaimKYCApplyRetries(ctx context.Context, limit int, lease time.Duration) ([]model.KYCApplyRetry, error)
	MarkKYCApplyRetrySucceeded(ctx context.Context, id uuid.UUID) error
	MarkKYCApplyRetryFailure(ctx context.Context, id uuid.UUID, retryCount int, nextAttemptAt time.Time, lastError string, dead bool) error

	// DowngradeKYCLevel applies the stricter ledger limits first, then lowers
	// auth_users. The callback is deliberately invoked before the DB tx.
	DowngradeKYCLevel(ctx context.Context, userID uuid.UUID, level int, decidedBy, reason string, applyTier func(context.Context, uuid.UUID, int) error) error
	CreateKYCDocument(context.Context, model.KYCDocument) error
	GetKYCDocument(context.Context, uuid.UUID) (model.KYCDocument, error)
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
			// Keep the original dependency error available to errors.Is while
			// making it possible for the facade to distinguish this rollback
			// from an auth DB failure.
			return fmt.Errorf("%w: %w", ErrKYCApplyTier, err)
		}
		var currentLevel int
		if err := tx.QueryRowContext(ctx, `SELECT kyc_level FROM auth_users WHERE id = $1 FOR UPDATE`, userID).Scan(&currentLevel); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("auth: lock user kyc level: %w", err)
		}
		if currentLevel+1 != level {
			return ErrKYCTierConflict
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
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO kyc_level_changes (id, user_id, from_level, to_level, direction, reason, decided_by)
			VALUES ($1, $2, $3, $4, 'upgrade', NULLIF($5, ''), $6)`,
			uuid.New(), userID, currentLevel, level, reason, decidedBy); err != nil {
			return fmt.Errorf("auth: record kyc upgrade: %w", err)
		}
		return nil
	})
}

func (r *repo) EnqueueKYCApplyRetry(ctx context.Context, retry model.KYCApplyRetry) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO kyc_apply_retries
			(id, submission_id, user_id, level, status, retry_count, next_attempt_at, last_error, direction, decided_by, decision_reason)
		VALUES ($1, NULLIF($2, '00000000-0000-0000-0000-000000000000')::uuid, $3, $4, 'pending', $5, $6, NULLIF($7, ''), COALESCE(NULLIF($8, ''), 'upgrade'), NULLIF($9, ''), NULLIF($10, ''))
		ON CONFLICT DO NOTHING`,
		retry.ID, retry.SubmissionID.String(), retry.UserID, retry.Level, retry.RetryCount,
		retry.NextAttemptAt, retry.LastError, retry.Direction, retry.DecidedBy, retry.DecisionReason)
	if err != nil {
		return fmt.Errorf("auth: enqueue kyc apply retry: %w", err)
	}
	return nil
}

const kycApplyRetryColumns = `id, submission_id, user_id, level, status, retry_count, next_attempt_at, last_error, locked_until, created_at, updated_at, direction, decided_by, decision_reason`

func scanKYCApplyRetry(scanner interface{ Scan(...any) error }) (model.KYCApplyRetry, error) {
	var retry model.KYCApplyRetry
	var submissionID sql.NullString
	var lastError, decidedBy, decisionReason sql.NullString
	var lockedUntil sql.NullTime
	if err := scanner.Scan(&retry.ID, &submissionID, &retry.UserID, &retry.Level,
		&retry.Status, &retry.RetryCount, &retry.NextAttemptAt, &lastError,
		&lockedUntil, &retry.CreatedAt, &retry.UpdatedAt, &retry.Direction, &decidedBy, &decisionReason); err != nil {
		return model.KYCApplyRetry{}, fmt.Errorf("auth: scan kyc apply retry: %w", err)
	}
	if lastError.Valid {
		retry.LastError = lastError.String
	}
	if submissionID.Valid {
		if parsed, err := uuid.Parse(submissionID.String); err == nil {
			retry.SubmissionID = parsed
		}
	}
	if decidedBy.Valid {
		retry.DecidedBy = decidedBy.String
	}
	if decisionReason.Valid {
		retry.DecisionReason = decisionReason.String
	}
	if lockedUntil.Valid {
		value := lockedUntil.Time
		retry.LockedUntil = &value
	}
	return retry, nil
}

func (r *repo) DowngradeKYCLevel(ctx context.Context, userID uuid.UUID, level int, decidedBy, reason string, applyTier func(context.Context, uuid.UUID, int) error) error {
	if level < 0 || level > 2 {
		return ErrKYCTierConflict
	}
	if err := applyTier(ctx, userID, level); err != nil {
		return fmt.Errorf("%w: %w", ErrKYCApplyTier, err)
	}
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var current int
		if err := tx.QueryRowContext(ctx, `SELECT kyc_level FROM auth_users WHERE id = $1 FOR UPDATE`, userID).Scan(&current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("auth: lock user for downgrade: %w", err)
		}
		if current <= level {
			return ErrKYCTierConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE auth_users SET kyc_level = $1, updated_at = now() WHERE id = $2 AND kyc_level > $1`, level, userID); err != nil {
			return fmt.Errorf("auth: downgrade kyc level: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO kyc_level_changes (id, user_id, from_level, to_level, direction, reason, decided_by)
			VALUES ($1, $2, $3, $4, 'downgrade', $5, $6)`,
			uuid.New(), userID, current, level, reason, decidedBy); err != nil {
			return fmt.Errorf("auth: record kyc downgrade: %w", err)
		}
		return nil
	})
}

func (r *repo) CreateKYCDocument(ctx context.Context, document model.KYCDocument) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO kyc_documents (id, submission_id, user_id, object_key, sha256, size_bytes, content_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, document.ID, document.SubmissionID, document.UserID,
		document.ObjectKey, document.SHA256, document.SizeBytes, document.ContentType)
	if err != nil {
		return fmt.Errorf("auth: create kyc document: %w", err)
	}
	return nil
}

func (r *repo) GetKYCDocument(ctx context.Context, id uuid.UUID) (model.KYCDocument, error) {
	var document model.KYCDocument
	err := r.db.QueryRowContext(ctx, `
		SELECT id, submission_id, user_id, object_key, sha256, size_bytes, content_type, created_at
		FROM kyc_documents WHERE id = $1`, id).Scan(&document.ID, &document.SubmissionID, &document.UserID,
		&document.ObjectKey, &document.SHA256, &document.SizeBytes, &document.ContentType, &document.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.KYCDocument{}, ErrNotFound
	}
	if err != nil {
		return model.KYCDocument{}, fmt.Errorf("auth: get kyc document: %w", err)
	}
	return document, nil
}

func (r *repo) ClaimKYCApplyRetries(ctx context.Context, limit int, lease time.Duration) ([]model.KYCApplyRetry, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if lease <= 0 {
		lease = 45 * time.Second
	}
	var out []model.KYCApplyRetry
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT `+kycApplyRetryColumns+`
			FROM kyc_apply_retries
			WHERE status = 'pending'
			  AND next_attempt_at <= now()
			  AND (locked_until IS NULL OR locked_until <= now())
			ORDER BY next_attempt_at ASC, id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT $1`, limit)
		if err != nil {
			return fmt.Errorf("auth: claim kyc apply retries query: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			retry, scanErr := scanKYCApplyRetry(rows)
			if scanErr != nil {
				return scanErr
			}
			out = append(out, retry)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("auth: claim kyc apply retries rows: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("auth: close kyc apply retries rows: %w", err)
		}
		if len(out) == 0 {
			return nil
		}
		for _, retry := range out {
			if _, err = tx.ExecContext(ctx, `
				UPDATE kyc_apply_retries
				SET locked_until = now() + $1::interval, updated_at = now()
				WHERE id = $2`, lease.String(), retry.ID); err != nil {
				return fmt.Errorf("auth: lease kyc apply retry %s: %w", retry.ID, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *repo) MarkKYCApplyRetrySucceeded(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE kyc_apply_retries
		SET status = 'succeeded', locked_until = NULL, updated_at = now()
		WHERE id = $1 AND status = 'pending'`, id)
	if err != nil {
		return fmt.Errorf("auth: mark kyc apply retry succeeded: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("auth: mark kyc apply retry succeeded rows: %w", err)
	} else if changed == 0 {
		return nil // idempotent acknowledgement after a worker retry
	}
	return nil
}

func (r *repo) MarkKYCApplyRetryFailure(ctx context.Context, id uuid.UUID, retryCount int, nextAttemptAt time.Time, lastError string, dead bool) error {
	status := "pending"
	if dead {
		status = "dead"
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE kyc_apply_retries
		SET status = $1, retry_count = $2, next_attempt_at = $3,
			last_error = NULLIF($4, ''), locked_until = NULL, updated_at = now()
		WHERE id = $5 AND status = 'pending'`, status, retryCount, nextAttemptAt, lastError, id)
	if err != nil {
		return fmt.Errorf("auth: mark kyc apply retry failure: %w", err)
	}
	return nil
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
