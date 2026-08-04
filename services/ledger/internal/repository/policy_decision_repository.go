package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

// PolicyDecisionRepository persists the preflight decision independently of
// the eventual ledger transaction. It is append-only and intentionally has no
// update or delete method.
type PolicyDecisionRepository interface {
	RecordPolicyDecision(context.Context, model.PolicyDecision) error
}

type policyDecisionRepo struct{ db database.DatabaseSQL }

func NewPolicyDecisionRepository(db database.DatabaseSQL) PolicyDecisionRepository {
	return &policyDecisionRepo{db: db}
}

func (r *policyDecisionRepo) RecordPolicyDecision(ctx context.Context, d model.PolicyDecision) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO money_movement_policy_decisions
		(id, actor_id, tenant_id, user_id, source, correlation_id, request_origin,
		 transaction_type, currency, amount_minor, allowed, reason, detail, effective_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		d.ID, nullableUUID(d.ActorID), nullableUUID(d.TenantID), nullableUUID(d.UserID), d.Source,
		d.CorrelationID, d.RequestOrigin, d.TransactionType, d.Currency, d.AmountMinor,
		d.Allowed, d.Reason, d.Detail, d.EffectiveAt)
	return err
}

func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
