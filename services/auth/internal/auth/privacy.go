package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/herdifirdausss/seev/internal/platform/errors"
	"github.com/herdifirdausss/seev/internal/platform/lifecycle/objectoutbox"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/services/auth/internal/auth/model"
)

var (
	ErrExportStorageUnavailable = errors.New("auth: export storage unavailable")
	ErrExportNotFound           = errors.New("auth: export not found")
	// ErrExportActiveAlready is never actually returned by RequestExport —
	// documented here because K9's own "at most one active export per
	// user" is enforced by returning the EXISTING active request instead
	// (true idempotent-create, matching this codebase's own established
	// idempotency conventions elsewhere), not by rejecting the second
	// call.
	ErrExportActiveAlready     = errors.New("auth: an export request is already active")
	ErrExportNotReady          = errors.New("auth: export is not ready for download")
	ErrExportAlreadyDownloaded = errors.New("auth: export has already been downloaded")
	ErrExportExpired           = errors.New("auth: export has expired")
)

// PrivacyRequest is the public-facing shape of one privacy_requests row —
// object_key/manifest_hash are deliberately NOT exposed here (internal
// storage detail, never returned to the API caller).
type PrivacyRequest struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Status        string
	SchemaVersion int
	Cutoff        time.Time
	RowCount      int
	ErrorMessage  string
	RequestedAt   time.Time
	ReadyAt       *time.Time
	ExpiresAt     *time.Time
	DownloadedAt  *time.Time
}

// SetExportKeyRing wires the internal/platform/security/crypto.Ring docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
// T4 (K9) export archives are encrypted with — dedicated key material,
// never the same ring as SetDocumentKeyRing (K2's own separate-key-material
// principle). A nil ring disables export creation
// (ErrExportStorageUnavailable), matching every other optional ring's
// nil-safe convention in this codebase.
func (m *Module) SetExportKeyRing(ring *cryptox.Ring) { m.exportRing = ring }

func exportAAD(requestID uuid.UUID) cryptox.AAD {
	return cryptox.AAD{Service: "auth", Table: "privacy_requests", Column: "object", RowID: requestID.String()}
}

// verifyPassword re-runs Login's own bcrypt comparison against the stored
// hash — reused (not reimplemented) for K9's "password re-verification"
// at both export creation and download time. Timing-equalized against a
// nonexistent-user the same way Login already is: GetPasswordHash only
// ever fails here for a data-integrity reason (the caller already proved
// userID is real via JWT), so there's no user-enumeration surface to
// equalize on this path.
func (m *Module) verifyPassword(ctx context.Context, userID uuid.UUID, password string) error {
	hash, err := m.users.GetPasswordHash(ctx, userID)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return ErrInvalidCredentials
	}
	return nil
}

// RequestExport creates a new export request after re-verifying the
// caller's password (K9: "requires an authenticated user and password
// re-verification"). Idempotent: a user with an existing active
// (pending/collecting) request gets THAT one back rather than a second
// (K9: "at most one active export is allowed per user") — enforced by
// `uq_privacy_requests_active_per_user`, not a check-then-insert race.
func (m *Module) RequestExport(ctx context.Context, userID uuid.UUID, password string) (PrivacyRequest, error) {
	if m.documentStore == nil || m.exportRing == nil {
		return PrivacyRequest{}, ErrExportStorageUnavailable
	}
	if err := m.verifyPassword(ctx, userID, password); err != nil {
		return PrivacyRequest{}, err
	}
	user, err := m.users.GetUserByID(ctx, userID)
	if err != nil {
		return PrivacyRequest{}, err
	}
	if user.Status != model.StatusActive {
		return PrivacyRequest{}, ErrUserDisabled
	}

	id := uuid.New()
	cutoff := time.Now().UTC()
	_, err = m.db.ExecContext(ctx, `
		INSERT INTO privacy_requests (id, user_id, status, request_type, schema_version, cutoff)
		VALUES ($1, $2, 'pending', 'export', 1, $3)`,
		id, userID, cutoff)
	if err != nil {
		if platformerrors.IsDuplicateKey(err) {
			return m.activeExport(ctx, userID)
		}
		return PrivacyRequest{}, fmt.Errorf("auth: create export request: %w", err)
	}
	return PrivacyRequest{ID: id, UserID: userID, Status: "pending", SchemaVersion: 1, Cutoff: cutoff, RequestedAt: cutoff}, nil
}

func (m *Module) activeExport(ctx context.Context, userID uuid.UUID) (PrivacyRequest, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT id, user_id, status, schema_version, cutoff, COALESCE(row_count,0), COALESCE(error_message,''),
		       requested_at, ready_at, expires_at, downloaded_at
		FROM privacy_requests WHERE user_id = $1 AND request_type = 'export' AND status IN ('pending','collecting')
		ORDER BY requested_at DESC LIMIT 1`, userID)
	return scanPrivacyRequest(row)
}

// GetExportStatus looks up a request by id, scoped to the caller's own
// user_id — a request belonging to another user is reported as
// ErrExportNotFound, never a distinct "forbidden," the same
// information-disclosure reasoning as CanAccessAccount and every other
// IDOR-safe lookup already in this module (don't confirm existence of
// another user's resource).
func (m *Module) GetExportStatus(ctx context.Context, userID, requestID uuid.UUID) (PrivacyRequest, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT id, user_id, status, schema_version, cutoff, COALESCE(row_count,0), COALESCE(error_message,''),
		       requested_at, ready_at, expires_at, downloaded_at
		FROM privacy_requests WHERE id = $1 AND user_id = $2 AND request_type = 'export'`, requestID, userID)
	return scanPrivacyRequest(row)
}

func scanPrivacyRequest(row *sql.Row) (PrivacyRequest, error) {
	var req PrivacyRequest
	if err := row.Scan(&req.ID, &req.UserID, &req.Status, &req.SchemaVersion, &req.Cutoff, &req.RowCount, &req.ErrorMessage,
		&req.RequestedAt, &req.ReadyAt, &req.ExpiresAt, &req.DownloadedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PrivacyRequest{}, ErrExportNotFound
		}
		return PrivacyRequest{}, fmt.Errorf("auth: get export request: %w", err)
	}
	return req, nil
}

// DownloadExport rechecks JWT ownership (via the userID parameter, already
// authenticated by the caller) AND password (K9: "download rechecks JWT
// ownership and password"), decrypts the archive in memory (never to
// disk — the caller streams these bytes straight into the HTTP response),
// and unconditionally enqueues the object for deletion via
// internal/platform/lifecycle/objectoutbox — successful download is exactly as eligible for
// cleanup as TTL expiry, and Enqueue's own ON CONFLICT DO NOTHING makes
// it safe to also run from the TTL sweep without double-enqueueing.
func (m *Module) DownloadExport(ctx context.Context, userID, requestID uuid.UUID, password string) ([]byte, error) {
	if m.documentStore == nil || m.exportRing == nil {
		return nil, ErrExportStorageUnavailable
	}
	if err := m.verifyPassword(ctx, userID, password); err != nil {
		return nil, err
	}

	var status, objectKey string
	var expiresAt sql.NullTime
	var downloadedAt sql.NullTime
	err := m.db.QueryRowContext(ctx, `
		SELECT status, COALESCE(object_key,''), expires_at, downloaded_at
		FROM privacy_requests WHERE id = $1 AND user_id = $2 AND request_type = 'export'`, requestID, userID,
	).Scan(&status, &objectKey, &expiresAt, &downloadedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrExportNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: lookup export for download: %w", err)
	}
	if downloadedAt.Valid {
		return nil, ErrExportAlreadyDownloaded
	}
	if status != "ready" || objectKey == "" {
		return nil, ErrExportNotReady
	}
	if expiresAt.Valid && time.Now().After(expiresAt.Time) {
		return nil, ErrExportExpired
	}

	encrypted, err := m.documentStore.Get(ctx, objectKey)
	if err != nil {
		return nil, ErrExportStorageUnavailable
	}
	plaintext, err := m.exportRing.Open(exportAAD(requestID), encrypted)
	if err != nil {
		return nil, fmt.Errorf("auth: decrypt export archive: %w", err)
	}

	err = m.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE privacy_requests SET downloaded_at = now(), updated_at = now() WHERE id = $1`, requestID); err != nil {
			return err
		}
		return objectoutbox.Enqueue(ctx, tx, "auth", "privacy_requests", requestID, objectKey)
	})
	if err != nil {
		privacyObjectDeleteTotal.WithLabelValues("export", "enqueue_failed").Inc()
		return nil, fmt.Errorf("auth: finalize export download: %w", err)
	}
	privacyObjectDeleteTotal.WithLabelValues("export", "enqueued").Inc()
	return plaintext, nil
}
