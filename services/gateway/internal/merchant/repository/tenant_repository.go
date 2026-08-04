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

type tenantRepository struct {
	db database.DatabaseSQL
}

func (r *tenantRepository) Create(ctx context.Context, t model.Tenant) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO merchant_tenants
			(id, public_id, external_code, name, environment, status, default_currency, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), now())`,
		t.ID, t.PublicID, t.ExternalCode, t.Name, t.Environment, t.Status, t.DefaultCurrency, t.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("merchant: create tenant: %w", err)
	}
	return nil
}

func (r *tenantRepository) GetByID(ctx context.Context, id uuid.UUID) (model.Tenant, error) {
	return r.scanOne(r.db.QueryRowContext(ctx, `
		SELECT id, public_id, external_code, name, environment, status, default_currency, primary_account_id,
		       created_by, activated_by, suspended_by, created_at, updated_at, activated_at, suspended_at, closed_at
		FROM merchant_tenants WHERE id = $1`, id))
}

func (r *tenantRepository) GetByPublicID(ctx context.Context, publicID string) (model.Tenant, error) {
	return r.scanOne(r.db.QueryRowContext(ctx, `
		SELECT id, public_id, external_code, name, environment, status, default_currency, primary_account_id,
		       created_by, activated_by, suspended_by, created_at, updated_at, activated_at, suspended_at, closed_at
		FROM merchant_tenants WHERE public_id = $1`, publicID))
}

func (r *tenantRepository) scanOne(row *sql.Row) (model.Tenant, error) {
	var t model.Tenant
	err := row.Scan(&t.ID, &t.PublicID, &t.ExternalCode, &t.Name, &t.Environment, &t.Status, &t.DefaultCurrency,
		&t.PrimaryAccountID, &t.CreatedBy, &t.ActivatedBy, &t.SuspendedBy, &t.CreatedAt, &t.UpdatedAt,
		&t.ActivatedAt, &t.SuspendedAt, &t.ClosedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Tenant{}, ErrNotFound
	}
	if err != nil {
		return model.Tenant{}, fmt.Errorf("merchant: scan tenant: %w", err)
	}
	return t, nil
}

// UpdateStatus is a generic status transition (draft->active,
// active->suspended, etc.) — T3/T8's operator flows apply the correct
// actor column (activated_by/suspended_by) and *_at timestamp per
// transition; this method only handles the shared status+updated_at
// write, matching status/actor semantics rather than re-implementing five
// near-identical UPDATE statements.
func (r *tenantRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status, actor string) error {
	var res sql.Result
	var err error
	switch status {
	case "active":
		res, err = r.db.ExecContext(ctx, `
			UPDATE merchant_tenants SET status = $1, activated_by = $2, activated_at = now(), updated_at = now()
			WHERE id = $3`, status, actor, id)
	case "suspended":
		res, err = r.db.ExecContext(ctx, `
			UPDATE merchant_tenants SET status = $1, suspended_by = $2, suspended_at = now(), updated_at = now()
			WHERE id = $3`, status, actor, id)
	case "closed":
		res, err = r.db.ExecContext(ctx, `
			UPDATE merchant_tenants SET status = $1, closed_at = now(), updated_at = now()
			WHERE id = $2`, status, id)
	default:
		res, err = r.db.ExecContext(ctx, `
			UPDATE merchant_tenants SET status = $1, updated_at = now() WHERE id = $2`, status, id)
	}
	if err != nil {
		return fmt.Errorf("merchant: update tenant status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *tenantRepository) SetPrimaryAccount(ctx context.Context, id uuid.UUID, accountID uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_tenants SET primary_account_id = $1, updated_at = now() WHERE id = $2`, accountID, id)
	if err != nil {
		return fmt.Errorf("merchant: set tenant primary account: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
