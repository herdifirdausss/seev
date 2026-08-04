package repository

//go:generate mockgen -source=chargeback_dispute_repository.go -destination=chargeback_dispute_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/contracts/events/ledger"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/database/identifiers"
	"github.com/herdifirdausss/seev/internal/platform/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

// ChargebackDisputeRepository persists chargeback case-management records
// (services/ledger/migrations/000035_chargeback_disputes) — deliberately separate
// from the `chargeback` processor's own money-movement transaction; a case
// can exist, gather evidence, and even resolve before any funds move, and a
// dispute_ref can be re-presented as a second case against the same charge.
type ChargebackDisputeRepository interface {
	CreateDispute(ctx context.Context, d model.ChargebackDispute) error
	GetDispute(ctx context.Context, id uuid.UUID) (model.ChargebackDispute, error)
	GetDisputeByRef(ctx context.Context, disputeRef string) (model.ChargebackDispute, error)
	ListDisputesByOriginalTx(ctx context.Context, originalTxID uuid.UUID) ([]model.ChargebackDispute, error)

	// ListOpenDisputes returns 'open'/'evidence_submitted' cases ordered by
	// evidence_due_at (soonest first, NULLs last) — the ops queue.
	ListOpenDisputes(ctx context.Context, limit, offset int) ([]model.ChargebackDispute, error)

	// SubmitEvidence transitions open -> evidence_submitted. The status guard
	// is in the WHERE clause (K3's atomic-UPDATE pattern, same as every other
	// status-guarded UPDATE in this package) — rows==0 tells the caller
	// whether the row didn't exist or wasn't in 'open'. changedBy is
	// recorded in chargeback_dispute_status_changes (services/ledger/migrations/000037)
	// in the SAME statement as the UPDATE — security audit finding: there
	// was previously no way to reconstruct who moved a case through its
	// lifecycle.
	SubmitEvidence(ctx context.Context, id uuid.UUID, evidenceRef, changedBy string) (int64, error)

	// ResolveDispute transitions open/evidence_submitted -> a terminal status
	// (won/lost/expired), stamping resolved_at/resolved_by/resolution_reason
	// together (the table's own CHECK ties these four together) and logging
	// the transition (including the actual PRIOR status, captured under
	// FOR UPDATE so it can't race with a concurrent transition) to
	// chargeback_dispute_status_changes.
	ResolveDispute(ctx context.Context, id uuid.UUID, status, resolvedBy, reason string) (int64, error)

	// LinkChargebackTx records the `chargeback` processor's transaction id
	// once its forced-debit money movement posts — an ops step that happens
	// independently of (and possibly before) the case's own resolution.
	LinkChargebackTx(ctx context.Context, id, chargebackTxID uuid.UUID) (int64, error)

	// ListStatusChanges returns a dispute's full transition history, oldest
	// first — the audit trail chargeback_dispute_status_changes exists for.
	ListStatusChanges(ctx context.Context, disputeID uuid.UUID) ([]model.ChargebackDisputeStatusChange, error)
}

type chargebackDisputeRepo struct {
	db     database.DatabaseSQL
	outbox OutboxRepository
}

func NewChargebackDisputeRepository(db database.DatabaseSQL) ChargebackDisputeRepository {
	return &chargebackDisputeRepo{db: db}
}

// NewChargebackDisputeRepositoryWithOutbox enables the transactional domain
// event path used by the running Ledger module. The compatibility constructor
// above remains available to schema-only callers that do not need cross-service
// notifications.
func NewChargebackDisputeRepositoryWithOutbox(db database.DatabaseSQL, outbox OutboxRepository) ChargebackDisputeRepository {
	return &chargebackDisputeRepo{db: db, outbox: outbox}
}

func (r *chargebackDisputeRepo) CreateDispute(ctx context.Context, d model.ChargebackDispute) error {
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO chargeback_disputes
				(id, original_tx_id, dispute_ref, card_network, reason_code, amount, currency, evidence_due_at, created_by)
			VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9)`,
			d.ID, d.OriginalTxID, d.DisputeRef, d.CardNetwork, d.ReasonCode,
			d.Amount.IntPart(), d.Currency, d.EvidenceDueAt, d.CreatedBy); err != nil {
			return err
		}
		if r.outbox == nil {
			return nil
		}
		d.Status = "open"
		return r.insertLifecycleEvent(ctx, tx, d, "", "open")
	})
	if err != nil {
		if platformerrors.IsDuplicateKey(err) {
			return fmt.Errorf("%w: dispute_ref %q already has a case", apperror.ErrValidation, d.DisputeRef)
		}
		return fmt.Errorf("create chargeback dispute: %w", err)
	}
	return nil
}

const chargebackDisputeColumns = `id, original_tx_id, chargeback_tx_id, dispute_ref, card_network,
	COALESCE(reason_code, ''), amount, currency, status, evidence_due_at, COALESCE(evidence_ref, ''),
	resolved_at, COALESCE(resolved_by, ''), COALESCE(resolution_reason, ''), created_by, created_at, updated_at`

func scanChargebackDispute(scan func(dest ...any) error) (model.ChargebackDispute, error) {
	var (
		d              model.ChargebackDispute
		amount         int64
		chargebackTxID uuid.NullUUID
		evidenceDueAt  sql.NullTime
		resolvedAt     sql.NullTime
	)
	err := scan(&d.ID, &d.OriginalTxID, &chargebackTxID, &d.DisputeRef, &d.CardNetwork,
		&d.ReasonCode, &amount, &d.Currency, &d.Status, &evidenceDueAt, &d.EvidenceRef,
		&resolvedAt, &d.ResolvedBy, &d.ResolutionReason, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return model.ChargebackDispute{}, err
	}
	d.Amount = decimal.NewFromInt(amount)
	if chargebackTxID.Valid {
		id := chargebackTxID.UUID
		d.ChargebackTxID = &id
	}
	if evidenceDueAt.Valid {
		t := evidenceDueAt.Time
		d.EvidenceDueAt = &t
	}
	if resolvedAt.Valid {
		t := resolvedAt.Time
		d.ResolvedAt = &t
	}
	return d, nil
}

func (r *chargebackDisputeRepo) GetDispute(ctx context.Context, id uuid.UUID) (model.ChargebackDispute, error) {
	d, err := scanChargebackDispute(r.db.QueryRowContext(ctx,
		`SELECT `+chargebackDisputeColumns+` FROM chargeback_disputes WHERE id = $1`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ChargebackDispute{}, fmt.Errorf("%w: %s", apperror.ErrChargebackDisputeNotFound, id)
	}
	if err != nil {
		return model.ChargebackDispute{}, fmt.Errorf("get chargeback dispute: %w", err)
	}
	return d, nil
}

func (r *chargebackDisputeRepo) GetDisputeByRef(ctx context.Context, disputeRef string) (model.ChargebackDispute, error) {
	d, err := scanChargebackDispute(r.db.QueryRowContext(ctx,
		`SELECT `+chargebackDisputeColumns+` FROM chargeback_disputes WHERE dispute_ref = $1`, disputeRef).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ChargebackDispute{}, fmt.Errorf("%w: %s", apperror.ErrChargebackDisputeNotFound, disputeRef)
	}
	if err != nil {
		return model.ChargebackDispute{}, fmt.Errorf("get chargeback dispute by ref: %w", err)
	}
	return d, nil
}

func (r *chargebackDisputeRepo) ListDisputesByOriginalTx(ctx context.Context, originalTxID uuid.UUID) ([]model.ChargebackDispute, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+chargebackDisputeColumns+` FROM chargeback_disputes WHERE original_tx_id = $1 ORDER BY created_at ASC`, originalTxID)
	if err != nil {
		return nil, fmt.Errorf("list chargeback disputes by original tx: %w", err)
	}
	defer rows.Close()
	return scanChargebackDisputeRows(rows)
}

func (r *chargebackDisputeRepo) ListOpenDisputes(ctx context.Context, limit, offset int) ([]model.ChargebackDispute, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+chargebackDisputeColumns+`
		FROM chargeback_disputes
		WHERE status IN ('open', 'evidence_submitted')
		ORDER BY evidence_due_at ASC NULLS LAST, created_at ASC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list open chargeback disputes: %w", err)
	}
	defer rows.Close()
	return scanChargebackDisputeRows(rows)
}

func scanChargebackDisputeRows(rows *sql.Rows) ([]model.ChargebackDispute, error) {
	var out []model.ChargebackDispute
	for rows.Next() {
		d, err := scanChargebackDispute(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan chargeback dispute: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chargeback disputes: %w", err)
	}
	return out, nil
}

func (r *chargebackDisputeRepo) SubmitEvidence(ctx context.Context, id uuid.UUID, evidenceRef, changedBy string) (int64, error) {
	var rows int64
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var current model.ChargebackDispute
		var err error
		current, err = r.getTransitionCandidate(ctx, tx, id, "open", true)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE chargeback_disputes
			SET status = 'evidence_submitted', evidence_ref = NULLIF($1, ''), updated_at = now()
			WHERE id = $2 AND status = 'open'
			  AND (evidence_due_at IS NULL OR evidence_due_at > now())`, evidenceRef, id)
		if err != nil {
			return err
		}
		rows, err = result.RowsAffected()
		if err != nil || rows == 0 {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO chargeback_dispute_status_changes
				(id, dispute_id, from_status, to_status, changed_by)
			VALUES ($1, $2, 'open', 'evidence_submitted', $3)`,
			identifiers.NewV7(), id, changedBy); err != nil {
			return err
		}
		if r.outbox != nil {
			return r.insertLifecycleEvent(ctx, tx, current, "open", "evidence_submitted")
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("submit chargeback dispute evidence: %w", err)
	}
	return rows, nil
}

func (r *chargebackDisputeRepo) ResolveDispute(ctx context.Context, id uuid.UUID, status, resolvedBy, reason string) (int64, error) {
	var rows int64
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		current, err := r.getTransitionCandidate(ctx, tx, id, "", false)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE chargeback_disputes
			SET status = $1, resolved_at = now(), resolved_by = $2,
				resolution_reason = NULLIF($3, ''), updated_at = now()
			WHERE id = $4 AND status IN ('open', 'evidence_submitted')`,
			status, resolvedBy, reason, id)
		if err != nil {
			return err
		}
		rows, err = result.RowsAffected()
		if err != nil || rows == 0 {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO chargeback_dispute_status_changes
				(id, dispute_id, from_status, to_status, changed_by, reason)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			identifiers.NewV7(), id, current.Status, status, resolvedBy, reason); err != nil {
			return err
		}
		if r.outbox != nil {
			return r.insertLifecycleEvent(ctx, tx, current, current.Status, status)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("resolve chargeback dispute: %w", err)
	}
	return rows, nil
}

// ExpireDueDisputes is the worker's idempotent deadline transition. It uses
// the same locked old/updated/logged shape as ResolveDispute, so a concurrent
// human resolution wins one row and the expiry retry records nothing twice.
func (r *chargebackDisputeRepo) ExpireDueDisputes(ctx context.Context, now time.Time, actor, reason string) (int64, error) {
	var rows int64
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		candidates, err := r.listDueTransitionCandidates(ctx, tx, now)
		if err != nil {
			return err
		}
		for _, current := range candidates {
			result, err := tx.ExecContext(ctx, `
				UPDATE chargeback_disputes
				SET status='expired', resolved_at=now(), resolved_by=$1,
					resolution_reason=$2, updated_at=now()
				WHERE id=$3 AND status IN ('open', 'evidence_submitted')`, actor, reason, current.ID)
			if err != nil {
				return err
			}
			changed, err := result.RowsAffected()
			if err != nil {
				return err
			}
			rows += changed
			if changed == 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO chargeback_dispute_status_changes
					(id, dispute_id, from_status, to_status, reason, changed_by)
				VALUES ($1, $2, $3, 'expired', $4, $5)`,
				identifiers.NewV7(), current.ID, current.Status, reason, actor); err != nil {
				return err
			}
			if r.outbox != nil {
				if err := r.insertLifecycleEvent(ctx, tx, current, current.Status, "expired"); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("expire due chargeback disputes: %w", err)
	}
	return rows, nil
}

// getTransitionCandidate locks the current mutable case row and returns the
// pre-transition snapshot used both for the audit record and the notification
// event. When openOnly is true it also applies the evidence deadline guard.
func (r *chargebackDisputeRepo) getTransitionCandidate(ctx context.Context, tx *sql.Tx, id uuid.UUID, status string, openOnly bool) (model.ChargebackDispute, error) {
	query := `SELECT ` + chargebackDisputeColumns + ` FROM chargeback_disputes WHERE id = $1`
	args := []any{id}
	if openOnly {
		query += ` AND status = 'open' AND (evidence_due_at IS NULL OR evidence_due_at > now())`
	} else if status == "" {
		query += ` AND status IN ('open', 'evidence_submitted')`
	} else {
		query += ` AND status = $2`
		args = append(args, status)
	}
	query += ` FOR UPDATE`
	return scanChargebackDispute(tx.QueryRowContext(ctx, query, args...).Scan)
}

func (r *chargebackDisputeRepo) listDueTransitionCandidates(ctx context.Context, tx *sql.Tx, now time.Time) ([]model.ChargebackDispute, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT `+chargebackDisputeColumns+`
		FROM chargeback_disputes
		WHERE status IN ('open', 'evidence_submitted')
		  AND evidence_due_at IS NOT NULL AND evidence_due_at <= $1
		ORDER BY evidence_due_at ASC, id ASC
		LIMIT 100
		FOR UPDATE SKIP LOCKED`, now)
	if err != nil {
		return nil, fmt.Errorf("list due chargeback disputes: %w", err)
	}
	defer rows.Close()
	return scanChargebackDisputeRows(rows)
}

func (r *chargebackDisputeRepo) insertLifecycleEvent(ctx context.Context, tx *sql.Tx, d model.ChargebackDispute, fromStatus, toStatus string) error {
	recipient, err := r.disputeRecipient(ctx, tx, d.OriginalTxID)
	if err != nil {
		return err
	}
	var recipientPtr *uuid.UUID
	if recipient != uuid.Nil {
		recipientPtr = &recipient
	}
	event := events.NewDisputeLifecycle(
		d.ID, d.OriginalTxID, recipientPtr, d.DisputeRef, d.CardNetwork, d.ReasonCode,
		d.Amount.String(), d.Currency, fromStatus, toStatus, d.EvidenceDueAt, time.Now().UTC(),
	)
	return r.outbox.InsertEvents(ctx, tx, []model.OutboxEvent{{
		AggregateType: "chargeback_dispute", AggregateID: d.ID,
		EventType: events.TypeDisputeLifecycle, Payload: event.ToPayload(),
	}})
}

// disputeRecipient resolves the user account involved in the original
// movement. The source side is preferred when both sides are user accounts;
// for merchant/system-to-user charges the destination user is selected. A
// missing user owner is valid for system-only disputes and produces an event
// with no notification recipient.
func (r *chargebackDisputeRepo) disputeRecipient(ctx context.Context, tx *sql.Tx, originalTxID uuid.UUID) (uuid.UUID, error) {
	var owner uuid.NullUUID
	err := tx.QueryRowContext(ctx, `
		SELECT a.owner_id
		FROM ledger_transactions t
		JOIN accounts a ON a.id = t.source_account_id OR a.id = t.destination_account_id
		WHERE t.id = $1 AND a.owner_type = 'user'
		ORDER BY CASE WHEN a.id = t.source_account_id THEN 0 ELSE 1 END, a.id
		LIMIT 1`, originalTxID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolve dispute notification recipient: %w", err)
	}
	return owner.UUID, nil
}

func (r *chargebackDisputeRepo) ListStatusChanges(ctx context.Context, disputeID uuid.UUID) ([]model.ChargebackDisputeStatusChange, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, dispute_id, from_status, to_status, reason, changed_by, created_at
		FROM chargeback_dispute_status_changes
		WHERE dispute_id = $1
		ORDER BY created_at ASC`, disputeID)
	if err != nil {
		return nil, fmt.Errorf("list chargeback dispute status changes: %w", err)
	}
	defer rows.Close()
	var out []model.ChargebackDisputeStatusChange
	for rows.Next() {
		var c model.ChargebackDisputeStatusChange
		if err := rows.Scan(&c.ID, &c.DisputeID, &c.FromStatus, &c.ToStatus, &c.Reason, &c.ChangedBy, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan chargeback dispute status change: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chargeback dispute status changes: %w", err)
	}
	return out, nil
}

func (r *chargebackDisputeRepo) LinkChargebackTx(ctx context.Context, id, chargebackTxID uuid.UUID) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE chargeback_disputes
		SET chargeback_tx_id = $1, updated_at = now()
		WHERE id = $2 AND chargeback_tx_id IS NULL`, chargebackTxID, id)
	if err != nil {
		return 0, fmt.Errorf("link chargeback transaction: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("link chargeback transaction rows: %w", err)
	}
	return rows, nil
}
