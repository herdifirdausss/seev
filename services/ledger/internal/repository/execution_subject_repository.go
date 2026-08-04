package repository

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

// ExecutionSubjectRepository is also the command.SubjectStateReader. The
// method signature is intentionally kept at the model boundary in command's
// implementation to avoid transport-specific authorization dependencies.
type ExecutionSubjectRepository interface {
	UpsertExecutionSubject(context.Context, model.ExecutionSubject) error
	GetExecutionSubject(context.Context, uuid.UUID, uuid.UUID) (model.ExecutionSubject, error)
}

type executionSubjectRepo struct{ db database.DatabaseSQL }

func NewExecutionSubjectRepository(db database.DatabaseSQL) ExecutionSubjectRepository {
	return &executionSubjectRepo{db: db}
}

func (r *executionSubjectRepo) UpsertExecutionSubject(ctx context.Context, subject model.ExecutionSubject) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO money_movement_execution_subjects
		(user_id, tenant_id, status, kyc_level, kyc_verified_until, tenant_status)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (user_id, tenant_id) DO UPDATE SET
		status=EXCLUDED.status, kyc_level=EXCLUDED.kyc_level,
		kyc_verified_until=EXCLUDED.kyc_verified_until,
		tenant_status=EXCLUDED.tenant_status, updated_at=now()`,
		subject.UserID, nullableUUID(subject.TenantID), subject.Status, subject.KYCLevel,
		subject.KYCVerifiedUntil, subject.TenantStatus)
	return err
}

func (r *executionSubjectRepo) GetExecutionSubject(ctx context.Context, userID, tenantID uuid.UUID) (model.ExecutionSubject, error) {
	var state model.ExecutionSubject
	var verifiedUntil sql.NullTime
	var tenantStatus string
	err := r.db.QueryRowContext(ctx, `
		SELECT status, kyc_level, kyc_verified_until, tenant_status
		FROM money_movement_execution_subjects
		WHERE user_id = $1 AND (tenant_id = $2 OR tenant_id IS NULL)
		ORDER BY tenant_id IS NOT NULL DESC
		LIMIT 1`, userID, nullableUUID(tenantID)).Scan(&state.Status, &state.KYCLevel, &verifiedUntil, &tenantStatus)
	if err != nil {
		return model.ExecutionSubject{}, err
	}
	if verifiedUntil.Valid {
		value := verifiedUntil.Time.UTC()
		state.KYCVerifiedUntil = &value
	}
	state.TenantStatus = tenantStatus
	return state, nil
}
