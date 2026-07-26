package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/pkg/generalerror"
)

// Package-level errors for docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
// T5's own work item 2 (K10): "Admin/operator accounts... require the
// operator offboarding runbook and maker/checker approval" — the gap
// ErrClosureNotSelfService's own doc comment named as A8 T5b.
var (
	ErrOperatorOffboardingNotFound = errors.New("auth: operator offboarding request not found")
	// ErrOperatorOffboardingNotOperator maps to 400 — this flow exists
	// specifically for admin/operator accounts; an ordinary user's own
	// account uses self-service RequestClosure instead.
	ErrOperatorOffboardingNotOperator = errors.New("auth: target account is not an admin/operator account")
	// ErrOperatorOffboardingSelfApproval maps to 403 — mirrors ledger's
	// own adjustments.ErrSelfApproval: the same identity that proposed an
	// offboarding can never also approve it (enforced here AND by the
	// migration's own CHECK constraint as the backstop).
	ErrOperatorOffboardingSelfApproval   = errors.New("auth: cannot approve or reject your own operator offboarding proposal")
	ErrOperatorOffboardingAlreadyDecided = errors.New("auth: operator offboarding request was already decided")
)

// OperatorOffboardingRequest is the maker-checker record — never the
// target subject's own PII beyond their id, since requested_by/approved_by
// are OPERATOR identities (email or admin user id), not the offboarded
// account's own data.
type OperatorOffboardingRequest struct {
	ID               uuid.UUID
	TargetUserID     uuid.UUID
	RequestedBy      string
	ApprovedBy       string
	Reason           string
	Status           string
	ClosureRequestID uuid.UUID
	CreatedAt        time.Time
	DecidedAt        *time.Time
}

// ProposeOperatorOffboarding is the "maker" half — any operator may
// propose closing an admin/operator account (never their own, in
// practice, since Approve below will reject self-approval, but Propose
// itself does not need to check that: the two-person gate is enforced at
// Approve). Idempotent: a target with an existing pending proposal gets
// that one reported back via the unique-violation-as-success convention
// this codebase already uses (RequestClosure, RequestExport).
func (m *Module) ProposeOperatorOffboarding(ctx context.Context, requestedBy string, targetUserID uuid.UUID, reason string) (OperatorOffboardingRequest, error) {
	if requestedBy == "" {
		return OperatorOffboardingRequest{}, fmt.Errorf("auth: requested_by (caller identity) is required")
	}
	if reason == "" {
		return OperatorOffboardingRequest{}, fmt.Errorf("auth: reason is required")
	}
	target, err := m.users.GetUserByID(ctx, targetUserID)
	if err != nil {
		return OperatorOffboardingRequest{}, err
	}
	if target.Role == model.RoleUser {
		return OperatorOffboardingRequest{}, ErrOperatorOffboardingNotOperator
	}

	id := uuid.New()
	createdAt := time.Now().UTC()
	_, err = m.db.ExecContext(ctx, `
		INSERT INTO auth_operator_offboarding_requests (id, target_user_id, requested_by, reason, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		id, targetUserID, requestedBy, reason, createdAt)
	if err != nil {
		if generalerror.IsDuplicateKey(err) {
			return m.pendingOperatorOffboarding(ctx, targetUserID)
		}
		return OperatorOffboardingRequest{}, fmt.Errorf("auth: create operator offboarding proposal: %w", err)
	}
	return OperatorOffboardingRequest{
		ID: id, TargetUserID: targetUserID, RequestedBy: requestedBy, Reason: reason,
		Status: "pending", CreatedAt: createdAt,
	}, nil
}

func (m *Module) pendingOperatorOffboarding(ctx context.Context, targetUserID uuid.UUID) (OperatorOffboardingRequest, error) {
	return scanOperatorOffboarding(m.db.QueryRowContext(ctx, `
		SELECT id, target_user_id, requested_by, COALESCE(approved_by,''), reason, status,
		       COALESCE(closure_request_id::text,''), created_at, decided_at
		FROM auth_operator_offboarding_requests WHERE target_user_id = $1 AND status = 'pending'`, targetUserID))
}

// GetOperatorOffboarding returns one request by id — no ownership scoping
// (unlike GetExportStatus/GetClosureStatus): this is an operator-facing
// admin endpoint, not an end-user one, gated by admin JWT + role at the
// HTTP layer instead.
func (m *Module) GetOperatorOffboarding(ctx context.Context, id uuid.UUID) (OperatorOffboardingRequest, error) {
	return scanOperatorOffboarding(m.db.QueryRowContext(ctx, `
		SELECT id, target_user_id, requested_by, COALESCE(approved_by,''), reason, status,
		       COALESCE(closure_request_id::text,''), created_at, decided_at
		FROM auth_operator_offboarding_requests WHERE id = $1`, id))
}

// ListOperatorOffboarding returns requests filtered by status (empty = all),
// newest first — mirrors ledger's own adjustments.Service.List.
func (m *Module) ListOperatorOffboarding(ctx context.Context, status string, limit int) ([]OperatorOffboardingRequest, error) {
	var rows *sql.Rows
	var err error
	if status == "" {
		rows, err = m.db.QueryContext(ctx, `
			SELECT id, target_user_id, requested_by, COALESCE(approved_by,''), reason, status,
			       COALESCE(closure_request_id::text,''), created_at, decided_at
			FROM auth_operator_offboarding_requests ORDER BY created_at DESC LIMIT $1`, limit)
	} else {
		rows, err = m.db.QueryContext(ctx, `
			SELECT id, target_user_id, requested_by, COALESCE(approved_by,''), reason, status,
			       COALESCE(closure_request_id::text,''), created_at, decided_at
			FROM auth_operator_offboarding_requests WHERE status = $1 ORDER BY created_at DESC LIMIT $2`, status, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("auth: list operator offboarding requests: %w", err)
	}
	defer rows.Close()

	var out []OperatorOffboardingRequest
	for rows.Next() {
		req, err := scanOperatorOffboardingRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

// ApproveOperatorOffboarding is the "checker" half. approvedBy must differ
// from the original requester — checked here for a clear typed error, AND
// enforced by the migration's own CHECK constraint as the backstop, same
// belt-and-suspenders discipline as ledger's adjustments.Service.Approve.
//
// Approval creates the exact same privacy_requests 'closure' row
// RequestClosure (T5) creates for self-service closure — same surrogate
// generation, same active-subject-ciphertext sealing under the SAME
// closure key ring, same auth_users status flip to 'closing' — so the
// existing closure saga worker (ProcessOnePendingClosure) and every
// registered owner (A8 T4b/T5b) drive the rest identically regardless of
// which path started it. The only difference from RequestClosure is the
// entry gate: a second operator's approval instead of the subject's own
// password, and no RoleUser-only restriction (that restriction is the
// entire reason this flow exists).
func (m *Module) ApproveOperatorOffboarding(ctx context.Context, id uuid.UUID, approvedBy string) (OperatorOffboardingRequest, error) {
	if m.closureRing == nil || len(m.closureOwners) == 0 {
		return OperatorOffboardingRequest{}, ErrClosureUnavailable
	}
	req, err := m.GetOperatorOffboarding(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return OperatorOffboardingRequest{}, ErrOperatorOffboardingNotFound
	}
	if err != nil {
		return OperatorOffboardingRequest{}, err
	}
	if req.RequestedBy == approvedBy {
		return OperatorOffboardingRequest{}, ErrOperatorOffboardingSelfApproval
	}
	if req.Status != "pending" {
		return OperatorOffboardingRequest{}, ErrOperatorOffboardingAlreadyDecided
	}

	closureID := uuid.New()
	surrogateID := uuid.New()
	ciphertext, err := m.closureRing.Seal(closureAAD(closureID), []byte(req.TargetUserID.String()))
	if err != nil {
		return OperatorOffboardingRequest{}, fmt.Errorf("auth: encrypt active subject: %w", err)
	}

	decidedAt := time.Now().UTC()
	err = m.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		// privacy_requests must exist before the offboarding row's own FK to
		// it is set, below — Postgres checks FK constraints immediately, not
		// at commit, so this insert has to come first within the
		// transaction.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO privacy_requests (id, user_id, status, request_type, surrogate_id, active_subject_ciphertext, schema_version, cutoff, requested_at)
			VALUES ($1, $2, 'pending', 'closure', $3, $4, 1, $5, $5)`,
			closureID, req.TargetUserID, surrogateID, ciphertext, decidedAt); err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx, `
			UPDATE auth_operator_offboarding_requests
			SET status = 'approved', approved_by = $1, closure_request_id = $2, decided_at = $3
			WHERE id = $4 AND status = 'pending'`,
			approvedBy, closureID, decidedAt, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrOperatorOffboardingAlreadyDecided
		}

		res2, err := tx.ExecContext(ctx, `UPDATE auth_users SET status = $1, updated_at = now() WHERE id = $2 AND status = $3`,
			model.StatusClosing, req.TargetUserID, model.StatusActive)
		if err != nil {
			return err
		}
		n2, err := res2.RowsAffected()
		if err != nil {
			return err
		}
		if n2 != 1 {
			return ErrUserDisabled
		}
		return nil
	})
	if err != nil {
		return OperatorOffboardingRequest{}, err
	}

	if err := m.refreshTokens.RevokeAllForUser(ctx, req.TargetUserID); err != nil {
		m.logger.Error("auth: revoke refresh tokens on operator offboarding approval failed", "error", err)
	}

	req.Status = "approved"
	req.ApprovedBy = approvedBy
	req.ClosureRequestID = closureID
	req.DecidedAt = &decidedAt
	return req, nil
}

// RejectOperatorOffboarding declines a pending proposal — no closure
// starts. approvedBy must differ from the requester, same as Approve.
func (m *Module) RejectOperatorOffboarding(ctx context.Context, id uuid.UUID, approvedBy string) (OperatorOffboardingRequest, error) {
	req, err := m.GetOperatorOffboarding(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return OperatorOffboardingRequest{}, ErrOperatorOffboardingNotFound
	}
	if err != nil {
		return OperatorOffboardingRequest{}, err
	}
	if req.RequestedBy == approvedBy {
		return OperatorOffboardingRequest{}, ErrOperatorOffboardingSelfApproval
	}
	if req.Status != "pending" {
		return OperatorOffboardingRequest{}, ErrOperatorOffboardingAlreadyDecided
	}

	decidedAt := time.Now().UTC()
	res, err := m.db.ExecContext(ctx, `
		UPDATE auth_operator_offboarding_requests
		SET status = 'rejected', approved_by = $1, decided_at = $2
		WHERE id = $3 AND status = 'pending'`, approvedBy, decidedAt, id)
	if err != nil {
		return OperatorOffboardingRequest{}, fmt.Errorf("auth: reject operator offboarding: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return OperatorOffboardingRequest{}, err
	}
	if n != 1 {
		return OperatorOffboardingRequest{}, ErrOperatorOffboardingAlreadyDecided
	}

	req.Status = "rejected"
	req.ApprovedBy = approvedBy
	req.DecidedAt = &decidedAt
	return req, nil
}

func scanOperatorOffboarding(row *sql.Row) (OperatorOffboardingRequest, error) {
	var req OperatorOffboardingRequest
	var closureRequestID string
	var decidedAt sql.NullTime
	if err := row.Scan(&req.ID, &req.TargetUserID, &req.RequestedBy, &req.ApprovedBy, &req.Reason,
		&req.Status, &closureRequestID, &req.CreatedAt, &decidedAt); err != nil {
		return OperatorOffboardingRequest{}, err
	}
	if closureRequestID != "" {
		req.ClosureRequestID = uuid.MustParse(closureRequestID)
	}
	if decidedAt.Valid {
		req.DecidedAt = &decidedAt.Time
	}
	return req, nil
}

func scanOperatorOffboardingRow(rows *sql.Rows) (OperatorOffboardingRequest, error) {
	var req OperatorOffboardingRequest
	var closureRequestID string
	var decidedAt sql.NullTime
	if err := rows.Scan(&req.ID, &req.TargetUserID, &req.RequestedBy, &req.ApprovedBy, &req.Reason,
		&req.Status, &closureRequestID, &req.CreatedAt, &decidedAt); err != nil {
		return OperatorOffboardingRequest{}, err
	}
	if closureRequestID != "" {
		req.ClosureRequestID = uuid.MustParse(closureRequestID)
	}
	if decidedAt.Valid {
		req.DecidedAt = &decidedAt.Time
	}
	return req, nil
}
