package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

// FeeRuleApprovalRepository owns the maker-checker lifecycle. It is separate
// from FeeRepository so existing quote/read mocks remain compatible while all
// production admin writes use this stronger contract.
type FeeRuleApprovalRepository interface {
	CreateFeeRuleDraft(context.Context, model.FeeRule, string, string) (model.FeeRuleVersion, error)
	SubmitFeeRule(context.Context, uuid.UUID, string) (model.FeeRuleVersion, error)
	ApproveFeeRule(context.Context, uuid.UUID, string, string) (model.FeeRuleVersion, error)
	RejectFeeRule(context.Context, uuid.UUID, string, string) (model.FeeRuleVersion, error)
	ListFeeRuleVersions(context.Context, uuid.UUID) ([]model.FeeRuleVersion, error)
}

type feeRuleApprovalRepo struct{ db database.DatabaseSQL }

func NewFeeRuleApprovalRepository(db database.DatabaseSQL) FeeRuleApprovalRepository {
	return &feeRuleApprovalRepo{db: db}
}

const feeRuleVersionColumns = `id, rule_id, version, tx_type, gateway, currency,
user_id, flat_minor_units, percent_basis_pts, fee_gateway, enabled, status,
created_by, COALESCE(submitted_by, ''), COALESCE(approved_by, ''),
COALESCE(rejected_by, ''), COALESCE(decision_reason, ''),
effective_from, effective_until, created_at, updated_at`

func scanFeeRuleVersion(scanner interface{ Scan(...any) error }) (model.FeeRuleVersion, error) {
	var v model.FeeRuleVersion
	err := scanner.Scan(&v.ID, &v.RuleID, &v.Version, &v.TxType, &v.Gateway, &v.Currency,
		&v.UserID, &v.FlatMinorUnits, &v.PercentBasisPts, &v.FeeGateway, &v.Enabled,
		&v.Status, &v.CreatedBy, &v.SubmittedBy, &v.ApprovedBy, &v.RejectedBy,
		&v.DecisionReason, &v.EffectiveFrom, &v.EffectiveUntil, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

func (r *feeRuleApprovalRepo) CreateFeeRuleDraft(ctx context.Context, rule model.FeeRule, actor, reason string) (model.FeeRuleVersion, error) {
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	return scanFeeRuleVersion(r.db.QueryRowContext(ctx, `
		WITH next_version AS (
			SELECT COALESCE(MAX(version), 0) + 1 AS version
			FROM fee_rule_versions WHERE rule_id = $1
		)
		INSERT INTO fee_rule_versions
		(id, rule_id, version, tx_type, gateway, currency, user_id, flat_minor_units,
		 percent_basis_pts, fee_gateway, enabled, status, created_by, decision_reason)
		SELECT $2, $1, version, $3,$4,$5,$6,$7,$8,$9,$10,'draft',$11,$12
		FROM next_version
		RETURNING `+feeRuleVersionColumns,
		rule.ID, uuid.New(), rule.TxType, rule.Gateway, rule.Currency, rule.UserID,
		rule.FlatMinorUnits, rule.PercentBasisPts, rule.FeeGateway, rule.Enabled, actor, reason))
}

func (r *feeRuleApprovalRepo) SubmitFeeRule(ctx context.Context, id uuid.UUID, actor string) (model.FeeRuleVersion, error) {
	return scanFeeRuleVersion(r.db.QueryRowContext(ctx, `
		UPDATE fee_rule_versions SET status='submitted', submitted_by=$2, updated_at=now()
		WHERE id=$1 AND status='draft'
		RETURNING `+feeRuleVersionColumns, id, actor))
}

func (r *feeRuleApprovalRepo) ApproveFeeRule(ctx context.Context, id uuid.UUID, checker, reason string) (model.FeeRuleVersion, error) {
	var result model.FeeRuleVersion
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var ruleID uuid.UUID
		if err := tx.QueryRowContext(ctx, `SELECT rule_id FROM fee_rule_versions WHERE id=$1 AND status='submitted' FOR UPDATE`, id).Scan(&ruleID); err != nil {
			return err
		}
		// Retiring the prior active projection is an auditable state change on
		// the old version. It closes its validity window before the new version
		// is approved, so the overlap trigger still protects concurrent scopes.
		if _, err := tx.ExecContext(ctx, `
			UPDATE fee_rule_versions SET status='retired', effective_until=now(), updated_at=now()
			WHERE rule_id=$1 AND status='approved' AND id <> $2 AND effective_until IS NULL`, ruleID, id); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `
			UPDATE fee_rule_versions SET status='approved', approved_by=$2, decision_reason=$3, updated_at=now()
			WHERE id=$1 AND status='submitted' AND created_by <> $2
			RETURNING `+feeRuleVersionColumns, id, checker, reason).Scan(
			&result.ID, &result.RuleID, &result.Version, &result.TxType, &result.Gateway, &result.Currency,
			&result.UserID, &result.FlatMinorUnits, &result.PercentBasisPts, &result.FeeGateway, &result.Enabled,
			&result.Status, &result.CreatedBy, &result.SubmittedBy, &result.ApprovedBy, &result.RejectedBy,
			&result.DecisionReason, &result.EffectiveFrom, &result.EffectiveUntil, &result.CreatedAt, &result.UpdatedAt); err != nil {
			return fmt.Errorf("approve fee rule: %w", err)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO fee_rules
			(id, tx_type, gateway, currency, user_id, flat_minor_units, percent_basis_pts,
			 fee_gateway, enabled, created_by, approved_by, rule_version, status, effective_from)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'active',$13)
			ON CONFLICT (id) DO UPDATE SET tx_type=EXCLUDED.tx_type, gateway=EXCLUDED.gateway,
			currency=EXCLUDED.currency, user_id=EXCLUDED.user_id, flat_minor_units=EXCLUDED.flat_minor_units,
			percent_basis_pts=EXCLUDED.percent_basis_pts, fee_gateway=EXCLUDED.fee_gateway,
			enabled=EXCLUDED.enabled, created_by=EXCLUDED.created_by, approved_by=EXCLUDED.approved_by,
			rule_version=EXCLUDED.rule_version, status='active', effective_from=EXCLUDED.effective_from,
			updated_at=now()`, result.RuleID, result.TxType, result.Gateway, result.Currency, result.UserID,
			result.FlatMinorUnits, result.PercentBasisPts, result.FeeGateway, result.Enabled,
			result.CreatedBy, result.ApprovedBy, result.Version, result.EffectiveFrom)
		return err
	})
	return result, err
}

func (r *feeRuleApprovalRepo) RejectFeeRule(ctx context.Context, id uuid.UUID, checker, reason string) (model.FeeRuleVersion, error) {
	return scanFeeRuleVersion(r.db.QueryRowContext(ctx, `
		UPDATE fee_rule_versions SET status='rejected', rejected_by=$2, decision_reason=$3, updated_at=now()
		WHERE id=$1 AND status='submitted' AND created_by <> $2
		RETURNING `+feeRuleVersionColumns, id, checker, reason))
}

func (r *feeRuleApprovalRepo) ListFeeRuleVersions(ctx context.Context, ruleID uuid.UUID) ([]model.FeeRuleVersion, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+feeRuleVersionColumns+` FROM fee_rule_versions WHERE rule_id=$1 ORDER BY version DESC`, ruleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.FeeRuleVersion, 0)
	for rows.Next() {
		item, scanErr := scanFeeRuleVersion(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
