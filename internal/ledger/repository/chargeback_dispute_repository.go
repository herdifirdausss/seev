package repository

//go:generate mockgen -source=chargeback_dispute_repository.go -destination=chargeback_dispute_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalerror"
)

// ChargebackDisputeRepository persists chargeback case-management records
// (migrations/ledger/000035_chargeback_disputes) — deliberately separate
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
	// recorded in chargeback_dispute_status_changes (migrations/ledger/000037)
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
	db database.DatabaseSQL
}

func NewChargebackDisputeRepository(db database.DatabaseSQL) ChargebackDisputeRepository {
	return &chargebackDisputeRepo{db: db}
}

func (r *chargebackDisputeRepo) CreateDispute(ctx context.Context, d model.ChargebackDispute) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO chargeback_disputes
			(id, original_tx_id, dispute_ref, card_network, reason_code, amount, currency, evidence_due_at, created_by)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9)`,
		d.ID, d.OriginalTxID, d.DisputeRef, d.CardNetwork, d.ReasonCode,
		d.Amount.IntPart(), d.Currency, d.EvidenceDueAt, d.CreatedBy)
	if err != nil {
		if generalerror.IsDuplicateKey(err) {
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
	err := r.db.QueryRowContext(ctx, `
		WITH updated AS (
			UPDATE chargeback_disputes
			SET status = 'evidence_submitted', evidence_ref = NULLIF($1, ''), updated_at = now()
			WHERE id = $2 AND status = 'open'
			RETURNING id
		), logged AS (
			INSERT INTO chargeback_dispute_status_changes (id, dispute_id, from_status, to_status, changed_by)
			SELECT gen_random_uuid(), id, 'open', 'evidence_submitted', $3 FROM updated
			RETURNING dispute_id
		)
		SELECT count(*) FROM updated`, evidenceRef, id, changedBy).Scan(&rows)
	if err != nil {
		return 0, fmt.Errorf("submit chargeback dispute evidence: %w", err)
	}
	return rows, nil
}

func (r *chargebackDisputeRepo) ResolveDispute(ctx context.Context, id uuid.UUID, status, resolvedBy, reason string) (int64, error) {
	var rows int64
	// `old` locks the row FOR UPDATE before `updated` writes it — the data
	// dependency (updated joins FROM old) forces that ordering, so the
	// status `logged` records is the genuine pre-transition value, not a
	// value that could have changed underneath a concurrent request.
	err := r.db.QueryRowContext(ctx, `
		WITH old AS (
			SELECT id, status FROM chargeback_disputes
			WHERE id = $4 AND status IN ('open', 'evidence_submitted')
			FOR UPDATE
		), updated AS (
			UPDATE chargeback_disputes cd
			SET status = $1, resolved_at = now(), resolved_by = $2, resolution_reason = NULLIF($3, ''), updated_at = now()
			FROM old WHERE cd.id = old.id
			RETURNING cd.id
		), logged AS (
			INSERT INTO chargeback_dispute_status_changes (id, dispute_id, from_status, to_status, changed_by, reason)
			SELECT gen_random_uuid(), old.id, old.status, $1, $2, COALESCE($3, '')
			FROM old JOIN updated ON updated.id = old.id
			RETURNING dispute_id
		)
		SELECT count(*) FROM updated`, status, resolvedBy, reason, id).Scan(&rows)
	if err != nil {
		return 0, fmt.Errorf("resolve chargeback dispute: %w", err)
	}
	return rows, nil
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
