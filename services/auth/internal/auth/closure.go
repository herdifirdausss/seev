package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/errors"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/services/auth/internal/auth/model"
)

var (
	// ErrClosureUnavailable maps to 503 — no closure key ring or ledger
	// client configured yet (same "optional outside production, fails at
	// first use" convention as ErrExportStorageUnavailable).
	ErrClosureUnavailable = errors.New("auth: account closure is not available right now")
	// ErrClosureOwnerUnavailable maps to 503 — an owner (ledger) call
	// failed transiently; the saga worker retries with backoff.
	ErrClosureOwnerUnavailable = errors.New("auth: a dependent service is unavailable")
	// ErrClosureNotSelfService maps to 403 — K10: "Admin/operator accounts
	// cannot use self-service closure; they require the operator
	// offboarding runbook and maker/checker approval" (A8 T5b: that runbook
	// flow is not implemented by this pass).
	ErrClosureNotSelfService = errors.New("auth: admin and operator accounts require the operator offboarding runbook, not self-service closure")
	ErrClosureNotFound       = errors.New("auth: closure request not found")
)

// closureAAD binds the active-subject ciphertext to this specific request
// row — the same per-row AAD discipline exportAAD already uses, so a
// ciphertext can never be replayed against a different closure request.
func closureAAD(requestID uuid.UUID) cryptox.AAD {
	return cryptox.AAD{Service: "auth", Table: "privacy_requests", Column: "active_subject", RowID: requestID.String()}
}

// SetClosureKeyRing wires the internal/platform/security/crypto.Ring the closure saga's
// active-subject ciphertext is sealed under — dedicated key material,
// never shared with exportRing/documentRing (K2). Nil-safe like every
// other optional ring in this module.
func (m *Module) SetClosureKeyRing(ring *cryptox.Ring) { m.closureRing = ring }

// closureOwnerRegistration pairs a K11 owner's name (used as its
// owner_checkpoints key and its privacyOwnerCallsTotal/{operation} metric
// label) with the client that reaches it.
type closureOwnerRegistration struct {
	name   string
	client OwnerClosureClient
}

// RegisterClosureOwner adds an owner to the closure saga's registry (A8
// T5b) — order is fixed at registration time (services/auth/cmd/auth/main.go's
// own wiring order) and IS the saga's owner-processing order, so a
// resumed saga's "which owners are already done" check
// (closureOwnerPhase) stays deterministic across restarts regardless of
// call order at any single tick. Not nil-safe to call twice for the same
// name (would register a duplicate, not overwrite) — services/auth/cmd/auth's
// own main.go is the only caller and registers each owner exactly once.
func (m *Module) RegisterClosureOwner(name string, client OwnerClosureClient) {
	m.closureOwners = append(m.closureOwners, closureOwnerRegistration{name: name, client: client})
}

// RequestClosure creates a new account-closure request after re-verifying
// the caller's password (K10). Idempotent: a user with an existing active
// closure (or export — they share uq_privacy_requests_active_per_user)
// request gets THAT one back rather than a second. Immediately disables
// new login (auth_users.status = 'closing' — Login/Refresh already reject
// any status != 'active' generically) and best-effort revokes live refresh
// tokens; the saga worker (ProcessOnePendingClosure) does everything else.
func (m *Module) RequestClosure(ctx context.Context, userID uuid.UUID, password string) (PrivacyRequest, error) {
	if m.closureRing == nil || len(m.closureOwners) == 0 {
		return PrivacyRequest{}, ErrClosureUnavailable
	}
	if err := m.verifyPassword(ctx, userID, password); err != nil {
		return PrivacyRequest{}, err
	}
	user, err := m.users.GetUserByID(ctx, userID)
	if err != nil {
		return PrivacyRequest{}, err
	}
	if user.Role != model.RoleUser {
		return PrivacyRequest{}, ErrClosureNotSelfService
	}
	// A 'closing' user is allowed past this check (unlike 'disabled') so a
	// second RequestClosure call for the same user reaches the INSERT
	// below and hits its duplicate-key path — returning the SAME active
	// request, not a false ErrUserDisabled. The transactional UPDATE
	// further down (status='active' -> 'closing') is the real gate for a
	// genuinely first-time call; a already-closed request has no INSERT
	// conflict to hit and reaches ErrUserDisabled via GetUserByID on any
	// SUBSEQUENT unrelated call once status is 'closed'.
	if user.Status != model.StatusActive && user.Status != model.StatusClosing {
		return PrivacyRequest{}, ErrUserDisabled
	}

	id := uuid.New()
	surrogateID := uuid.New()
	ciphertext, err := m.closureRing.Seal(closureAAD(id), []byte(userID.String()))
	if err != nil {
		return PrivacyRequest{}, fmt.Errorf("auth: encrypt active subject: %w", err)
	}

	requestedAt := time.Now().UTC()
	err = m.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO privacy_requests (id, user_id, status, request_type, surrogate_id, active_subject_ciphertext, schema_version, cutoff, requested_at)
			VALUES ($1, $2, 'pending', 'closure', $3, $4, 1, $5, $5)`,
			id, userID, surrogateID, ciphertext, requestedAt); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE auth_users SET status = $1, updated_at = now() WHERE id = $2 AND status = $3`,
			model.StatusClosing, userID, model.StatusActive)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			// Raced by a concurrent status change (e.g. an admin disable)
			// between the GetUserByID check above and here.
			return ErrUserDisabled
		}
		return nil
	})
	if err != nil {
		if platformerrors.IsDuplicateKey(err) {
			return m.activePrivacyRequest(ctx, userID)
		}
		return PrivacyRequest{}, fmt.Errorf("auth: create closure request: %w", err)
	}
	if err := m.syncExecutionSubject(ctx, userID, model.StatusClosing, user.KYCLevel, user.KYCVerifiedUntil); err != nil {
		return PrivacyRequest{}, fmt.Errorf("auth: synchronize closing execution subject: %w", err)
	}

	// Best-effort: Login/Refresh already reject on status='closing' above,
	// so a live refresh token is unusable regardless of whether this
	// specific call succeeds — this is defense in depth, not the sole
	// enforcement point.
	if err := m.refreshTokens.RevokeAllForUser(ctx, userID); err != nil {
		m.logger.Error("auth: revoke refresh tokens on closure request failed", "error", err)
	}

	return PrivacyRequest{ID: id, UserID: userID, Status: "pending", SchemaVersion: 1, Cutoff: requestedAt, RequestedAt: requestedAt}, nil
}

func (m *Module) activePrivacyRequest(ctx context.Context, userID uuid.UUID) (PrivacyRequest, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT id, user_id, status, schema_version, cutoff, COALESCE(row_count,0), COALESCE(error_message,''),
		       requested_at, ready_at, expires_at, downloaded_at
		FROM privacy_requests WHERE user_id = $1 AND status IN ('pending','collecting','preparing','committing')
		ORDER BY requested_at DESC LIMIT 1`, userID)
	return scanPrivacyRequest(row)
}

// GetClosureStatus looks up a closure request by id, scoped to the
// caller's own user_id — same IDOR-safe "not found, never forbidden"
// convention as GetExportStatus.
func (m *Module) GetClosureStatus(ctx context.Context, userID, requestID uuid.UUID) (PrivacyRequest, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT id, user_id, status, schema_version, cutoff, COALESCE(row_count,0), COALESCE(error_message,''),
		       requested_at, ready_at, expires_at, downloaded_at
		FROM privacy_requests WHERE id = $1 AND user_id = $2 AND request_type = 'closure'`, requestID, userID)
	req, err := scanPrivacyRequest(row)
	if errors.Is(err, ErrExportNotFound) {
		return PrivacyRequest{}, ErrClosureNotFound
	}
	return req, err
}
