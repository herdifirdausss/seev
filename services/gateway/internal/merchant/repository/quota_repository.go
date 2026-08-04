package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/model"
)

type quotaRepository struct {
	db database.DatabaseSQL
}

func (r *quotaRepository) Upsert(ctx context.Context, p model.QuotaPolicy) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO merchant_quota_policies (id, tenant_id, quota_class, requests_per_minute, burst, is_enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), now())
		ON CONFLICT (tenant_id, quota_class) DO UPDATE SET
			requests_per_minute = EXCLUDED.requests_per_minute,
			burst = EXCLUDED.burst,
			is_enabled = EXCLUDED.is_enabled,
			updated_at = now()`,
		p.ID, p.TenantID, p.QuotaClass, p.RequestsPerMinute, p.Burst, p.IsEnabled,
	)
	if err != nil {
		return fmt.Errorf("merchant: upsert quota policy: %w", err)
	}
	return nil
}

func (r *quotaRepository) GetByTenantAndClass(ctx context.Context, tenantID uuid.UUID, quotaClass string) (model.QuotaPolicy, error) {
	var p model.QuotaPolicy
	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, quota_class, requests_per_minute, burst, is_enabled, created_at, updated_at
		FROM merchant_quota_policies WHERE tenant_id = $1 AND quota_class = $2`, tenantID, quotaClass,
	).Scan(&p.ID, &p.TenantID, &p.QuotaClass, &p.RequestsPerMinute, &p.Burst, &p.IsEnabled, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.QuotaPolicy{}, ErrNotFound
	}
	if err != nil {
		return model.QuotaPolicy{}, fmt.Errorf("merchant: get quota policy: %w", err)
	}
	return p, nil
}
