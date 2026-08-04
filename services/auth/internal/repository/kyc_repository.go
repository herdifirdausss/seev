package repository

//go:generate mockgen -source=kyc_repository.go -destination=kyc_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/errors"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/services/auth/internal/auth/model"
)

// KYCRepository persists KYC submissions, decisions, the durable
// apply-retry queue, and uploaded document metadata.
type KYCRepository interface {
	CreateKYCSubmission(ctx context.Context, s model.KYCSubmission) error
	GetLatestKYCSubmission(ctx context.Context, userID uuid.UUID) (model.KYCSubmission, error)
	GetKYCSubmission(ctx context.Context, id uuid.UUID) (model.KYCSubmission, error)
	ListKYCSubmissions(ctx context.Context, status string) ([]model.KYCSubmission, error)
	ListKYCRescreenSubjects(ctx context.Context, limit int) ([]model.KYCRescreenSubject, error)
	// ApproveKYCSubmission runs applyTier while the pending row is locked and
	// commits the auth level/submission decision only after it succeeds.
	// validUntil becomes auth_users.kyc_verified_until — the deadline the
	// periodic expiry worker (services/auth/internal/worker/expiry.go) downgrades
	// against.
	ApproveKYCSubmission(ctx context.Context, id uuid.UUID, decidedBy, providerRef, reason string, validUntil time.Time, applyTier func(context.Context, uuid.UUID, int) error) error
	RejectKYCSubmission(ctx context.Context, id uuid.UUID, decidedBy, reason string) error
	// ListExpiredKYCUsers returns ids of users whose current kyc_level's
	// validity window (kyc_verified_until) has passed — the periodic expiry
	// worker's source query.
	ListExpiredKYCUsers(ctx context.Context, limit int) ([]uuid.UUID, error)

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

type kycRepo struct {
	db   database.DatabaseSQL
	ring *cryptox.Ring
}

// NewKYCRepository's ring is REQUIRED — docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
// "A8 T2.5b" (the contract migration): kyc_submissions.payload has no
// plaintext column anymore (services/auth/migrations/000014_cryptox_contract), so
// every read/write here needs the ring to function at all. No separate
// lookup key: nothing looks up a submission by payload equality.
func NewKYCRepository(db database.DatabaseSQL, ring *cryptox.Ring) KYCRepository {
	if ring == nil {
		panic("auth: NewKYCRepository requires a non-nil cryptox ring")
	}
	return &kycRepo{db: db, ring: ring}
}

func payloadAAD(submissionID uuid.UUID) cryptox.AAD {
	return cryptox.AAD{Service: "auth", Table: "kyc_submissions", Column: "payload", RowID: submissionID.String()}
}

// extractRescreenFields mirrors the exact SQL this replaced —
// COALESCE(NULLIF(payload->>'name',”), NULLIF(payload->>'full_name',”))
// and payload->>'birth_date' — so ListKYCRescreenSubjects's own filter
// semantics (a submission with neither field is not rescreen-eligible)
// carry over unchanged; only the fields' storage moved from a live JSONB
// path query to columns populated at write time.
func extractRescreenFields(payload map[string]any) (name *string, birthDate string) {
	if v, ok := payload["name"].(string); ok && v != "" {
		name = &v
	} else if v, ok := payload["full_name"].(string); ok && v != "" {
		name = &v
	}
	if v, ok := payload["birth_date"].(string); ok {
		birthDate = v
	}
	return name, birthDate
}

const kycSubmissionColumns = `id, user_id, level_requested, status, provider, provider_ref, decided_by, decision_reason, created_at, decided_at, payload_ciphertext`

func (r *kycRepo) scanKYCSubmission(scanner interface{ Scan(...any) error }) (model.KYCSubmission, error) {
	var s model.KYCSubmission
	var payloadCiphertext []byte
	var providerRef, decidedBy, reason sql.NullString
	var decidedAt sql.NullTime
	if err := scanner.Scan(&s.ID, &s.UserID, &s.LevelRequested, &s.Status, &s.Provider, &providerRef, &decidedBy, &reason, &s.CreatedAt, &decidedAt, &payloadCiphertext); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.KYCSubmission{}, ErrKYCSubmissionNotFound
		}
		return model.KYCSubmission{}, fmt.Errorf("auth: scan kyc submission: %w", err)
	}
	payload, err := r.ring.Open(payloadAAD(s.ID), payloadCiphertext)
	if err != nil {
		return model.KYCSubmission{}, fmt.Errorf("auth: decrypt kyc payload: %w", err)
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

func (r *kycRepo) CreateKYCSubmission(ctx context.Context, s model.KYCSubmission) error {
	payload, err := s.MarshalPayload()
	if err != nil {
		return fmt.Errorf("auth: encode kyc payload: %w", err)
	}
	rescreenName, _ := extractRescreenFields(s.Payload)

	payloadCiphertext, err := r.ring.Seal(payloadAAD(s.ID), payload)
	if err != nil {
		return fmt.Errorf("auth: encrypt kyc payload: %w", err)
	}
	v := r.ring.CurrentVersion()

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO kyc_submissions (id, user_id, level_requested, status, provider,
			payload_ciphertext, payload_key_version, rescreen_eligible)
		VALUES ($1, $2, $3, 'pending', $4, $5, $6, $7)`,
		s.ID, s.UserID, s.LevelRequested, s.Provider,
		payloadCiphertext, v, rescreenName != nil)
	if err != nil {
		if platformerrors.IsDuplicateKey(err) {
			return ErrKYCSubmissionNotPending
		}
		return fmt.Errorf("auth: create kyc submission: %w", err)
	}
	return nil
}

func (r *kycRepo) GetLatestKYCSubmission(ctx context.Context, userID uuid.UUID) (model.KYCSubmission, error) {
	return r.scanKYCSubmission(r.db.QueryRowContext(ctx, `SELECT `+kycSubmissionColumns+` FROM kyc_submissions WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, userID))
}

func (r *kycRepo) GetKYCSubmission(ctx context.Context, id uuid.UUID) (model.KYCSubmission, error) {
	return r.scanKYCSubmission(r.db.QueryRowContext(ctx, `SELECT `+kycSubmissionColumns+` FROM kyc_submissions WHERE id = $1`, id))
}

func (r *kycRepo) ListKYCSubmissions(ctx context.Context, status string) ([]model.KYCSubmission, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+kycSubmissionColumns+` FROM kyc_submissions WHERE ($1 = '' OR status = $1) ORDER BY created_at ASC, id ASC`, status)
	if err != nil {
		return nil, fmt.Errorf("auth: list kyc submissions: %w", err)
	}
	defer rows.Close()
	var out []model.KYCSubmission
	for rows.Next() {
		s, scanErr := r.scanKYCSubmission(rows)
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

// ListKYCRescreenSubjects returns the latest approved subject projection per
// user. Only the fields required by the sanctions rule are selected; the
// original KYC payload remains inside auth-service. The database exposes
// only a non-sensitive rescreen_eligible boolean; name and birth date are
// decrypted from payload_ciphertext inside this repository.
func (r *kycRepo) ListKYCRescreenSubjects(ctx context.Context, limit int) ([]model.KYCRescreenSubject, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT ON (user_id) id,user_id,payload_ciphertext
		FROM kyc_submissions
		WHERE status = 'approved'
		  AND rescreen_eligible
		ORDER BY user_id, decided_at DESC NULLS LAST, created_at DESC, id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("auth: list kyc rescreen subjects: %w", err)
	}
	defer rows.Close()
	var subjects []model.KYCRescreenSubject
	for rows.Next() {
		var submissionID uuid.UUID
		var subject model.KYCRescreenSubject
		var ciphertext []byte
		if err := rows.Scan(&submissionID, &subject.UserID, &ciphertext); err != nil {
			return nil, fmt.Errorf("auth: scan kyc rescreen subject: %w", err)
		}
		payloadJSON, err := r.ring.Open(payloadAAD(submissionID), ciphertext)
		if err != nil {
			return nil, fmt.Errorf("auth: decrypt kyc rescreen payload: %w", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(payloadJSON, &payload); err != nil {
			return nil, fmt.Errorf("auth: decode kyc rescreen payload: %w", err)
		}
		name, birthDate := extractRescreenFields(payload)
		if name == nil {
			return nil, errors.New("auth: rescreen-eligible payload has no name")
		}
		subject.Name = *name
		subject.BirthDate = birthDate
		subjects = append(subjects, subject)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: iterate kyc rescreen subjects: %w", err)
	}
	return subjects, nil
}

// ListExpiredKYCUsers returns users above L0 whose kyc_verified_until has
// passed. Unlocked read — the expiry worker's distributed lock already
// serializes RunOnce across replicas (mirrors ListKYCRescreenSubjects), and
// DowngradeKYCLevel re-checks/locks the row itself before writing.
func (r *kycRepo) ListExpiredKYCUsers(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM auth_users
		WHERE kyc_level > 0 AND kyc_verified_until IS NOT NULL AND kyc_verified_until <= now()
		ORDER BY kyc_verified_until ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("auth: list expired kyc users: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("auth: scan expired kyc user: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: iterate expired kyc users: %w", err)
	}
	return out, nil
}

func (r *kycRepo) ApproveKYCSubmission(ctx context.Context, id uuid.UUID, decidedBy, providerRef, reason string, validUntil time.Time, applyTier func(context.Context, uuid.UUID, int) error) error {
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
		result, err := tx.ExecContext(ctx, `UPDATE auth_users SET kyc_level = $1, updated_at = now(), kyc_verified_until = $3 WHERE id = $2 AND kyc_level + 1 = $1`, level, userID, validUntil)
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

func (r *kycRepo) EnqueueKYCApplyRetry(ctx context.Context, retry model.KYCApplyRetry) error {
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

func (r *kycRepo) DowngradeKYCLevel(ctx context.Context, userID uuid.UUID, level int, decidedBy, reason string, applyTier func(context.Context, uuid.UUID, int) error) error {
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
		// kyc_verified_until always clears on downgrade — even a partial
		// downgrade (e.g. 2->1) has no record of when the SURVIVING level was
		// itself independently verified, so only a fresh approval sets a new
		// deadline (services/auth/migrations/000018_kyc_expiry.up.sql).
		if _, err := tx.ExecContext(ctx, `UPDATE auth_users SET kyc_level = $1, updated_at = now(), kyc_verified_until = NULL WHERE id = $2 AND kyc_level > $1`, level, userID); err != nil {
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

func (r *kycRepo) CreateKYCDocument(ctx context.Context, document model.KYCDocument) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO kyc_documents (id, submission_id, user_id, object_key, sha256, size_bytes, content_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, document.ID, document.SubmissionID, document.UserID,
		document.ObjectKey, document.SHA256, document.SizeBytes, document.ContentType)
	if err != nil {
		return fmt.Errorf("auth: create kyc document: %w", err)
	}
	return nil
}

func (r *kycRepo) GetKYCDocument(ctx context.Context, id uuid.UUID) (model.KYCDocument, error) {
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

func (r *kycRepo) ClaimKYCApplyRetries(ctx context.Context, limit int, lease time.Duration) ([]model.KYCApplyRetry, error) {
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

func (r *kycRepo) MarkKYCApplyRetrySucceeded(ctx context.Context, id uuid.UUID) error {
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

func (r *kycRepo) MarkKYCApplyRetryFailure(ctx context.Context, id uuid.UUID, retryCount int, nextAttemptAt time.Time, lastError string, dead bool) error {
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

func (r *kycRepo) RejectKYCSubmission(ctx context.Context, id uuid.UUID, decidedBy, reason string) error {
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
