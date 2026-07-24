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

	"github.com/herdifirdausss/seev/pkg/cryptox"
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

	// BackfillOnce is docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.5's bounded backfill
	// for sessions.email — same shape as every other repository's own
	// BackfillOnce. In practice sessions have a short TTL and the
	// existing retention job already purges expired ones (docs/roadmap/active/51 T1.5),
	// so this only ever has to catch currently-active pre-expand-phase
	// rows, never the full historical volume the other targets do.
	BackfillOnce(ctx context.Context, batchSize int) (int, error)
}

type sessionRepo struct {
	db   database.DatabaseSQL
	ring *cryptox.Ring
}

// NewSessionRepository's ring is docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.4's
// K2/K3 expand-phase encryption for sessions.email — same nil-safe
// optionality as internal/auth/repository's own ring parameters: nil
// means every read/write behaves exactly as before this task.
func NewSessionRepository(db database.DatabaseSQL, ring *cryptox.Ring) SessionRepository {
	return &sessionRepo{db: db, ring: ring}
}

func sessionEmailAAD(sessionID string) cryptox.AAD {
	return cryptox.AAD{Service: "adminbff", Table: "sessions", Column: "email", RowID: sessionID}
}

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
	var emailCiphertext []byte
	var emailKeyVersion *int
	if r.ring != nil {
		var err error
		if emailCiphertext, err = r.ring.Seal(sessionEmailAAD(s.ID), []byte(s.Email)); err != nil {
			return fmt.Errorf("adminbff: encrypt session email: %w", err)
		}
		v := r.ring.CurrentVersion()
		emailKeyVersion = &v
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sessions
			(id, user_id, email, role, csrf_token, created_at, last_seen_at, expires_at, absolute_expires_at,
			 email_ciphertext, email_key_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		s.ID, s.UserID, s.Email, s.Role, s.CSRFToken, s.CreatedAt, s.LastSeenAt, s.ExpiresAt, s.AbsoluteExpiresAt,
		emailCiphertext, emailKeyVersion)
	if err != nil {
		return fmt.Errorf("adminbff: create session: %w", err)
	}
	return nil
}

func (r *sessionRepo) GetSession(ctx context.Context, id string) (Session, error) {
	var s Session
	var emailCiphertext []byte
	var emailKeyVersion *int
	err := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, email, role, csrf_token, created_at, last_seen_at, expires_at, absolute_expires_at,
		       email_ciphertext, email_key_version
		FROM sessions
		WHERE id = $1 AND expires_at > now() AND absolute_expires_at > now()`, id).
		Scan(&s.ID, &s.UserID, &s.Email, &s.Role, &s.CSRFToken, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt, &s.AbsoluteExpiresAt,
			&emailCiphertext, &emailKeyVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("adminbff: get session: %w", err)
	}
	// Dual-read (K3): ciphertext wins when present (already backfilled);
	// otherwise the plaintext email column already scanned above stands.
	if r.ring != nil && emailCiphertext != nil {
		plain, err := r.ring.Open(sessionEmailAAD(s.ID), emailCiphertext)
		if err != nil {
			return Session{}, fmt.Errorf("adminbff: decrypt session email: %w", err)
		}
		s.Email = string(plain)
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

// DeleteSession calls the fn_delete_session SECURITY DEFINER function
// (migrations/adminbff/000004_session_delete_fn.up.sql) instead of issuing a
// direct DELETE: app_service (and therefore adminbff_app) is only ever
// granted SELECT, INSERT, UPDATE on sessions, so a direct DELETE fails with
// "permission denied for table sessions".
func (r *sessionRepo) BackfillOnce(ctx context.Context, batchSize int) (int, error) {
	if r.ring == nil {
		return 0, fmt.Errorf("adminbff: cryptox ring not configured, cannot backfill")
	}
	type pendingRow struct {
		id    string
		email string
	}
	var rows []pendingRow
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		result, queryErr := tx.QueryContext(ctx, `
			SELECT id, email FROM sessions
			WHERE email_ciphertext IS NULL
			ORDER BY created_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED`, batchSize)
		if queryErr != nil {
			return fmt.Errorf("select backfill batch: %w", queryErr)
		}
		for result.Next() {
			var pr pendingRow
			if scanErr := result.Scan(&pr.id, &pr.email); scanErr != nil {
				result.Close()
				return fmt.Errorf("scan backfill row: %w", scanErr)
			}
			rows = append(rows, pr)
		}
		if rowsErr := result.Err(); rowsErr != nil {
			result.Close()
			return fmt.Errorf("iterate backfill batch: %w", rowsErr)
		}
		result.Close()

		v := r.ring.CurrentVersion()
		for _, pr := range rows {
			ciphertext, sealErr := r.ring.Seal(sessionEmailAAD(pr.id), []byte(pr.email))
			if sealErr != nil {
				return fmt.Errorf("encrypt session email for backfill %s: %w", pr.id, sealErr)
			}
			if _, execErr := tx.ExecContext(ctx, `
				UPDATE sessions SET email_ciphertext = $1, email_key_version = $2 WHERE id = $3`,
				ciphertext, v, pr.id); execErr != nil {
				return fmt.Errorf("update backfilled session %s: %w", pr.id, execErr)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

func (r *sessionRepo) DeleteSession(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `SELECT fn_delete_session($1)`, id); err != nil {
		return fmt.Errorf("adminbff: delete session: %w", err)
	}
	return nil
}
