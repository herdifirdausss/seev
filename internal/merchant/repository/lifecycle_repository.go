package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

type lifecycleRepository struct {
	db database.DatabaseSQL
}

func (r *lifecycleRepository) Create(ctx context.Context, req model.TenantLifecycleRequest) (bool, model.TenantLifecycleRequest, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO merchant_tenant_lifecycle_requests (id, tenant_id, action, requested_by, reason, status, created_at)
		VALUES ($1, $2, $3, $4, $5, 'pending', now())
		ON CONFLICT (tenant_id, action) WHERE status = 'pending' DO NOTHING`,
		req.ID, req.TenantID, req.Action, req.RequestedBy, req.Reason,
	)
	if err != nil {
		return false, model.TenantLifecycleRequest{}, fmt.Errorf("merchant: create tenant lifecycle request: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, model.TenantLifecycleRequest{}, nil
	}
	existing, found, err := r.GetPending(ctx, req.TenantID, req.Action)
	if err != nil {
		return false, model.TenantLifecycleRequest{}, err
	}
	if !found {
		return false, model.TenantLifecycleRequest{}, fmt.Errorf("merchant: tenant lifecycle request insert conflicted but no pending row found")
	}
	return false, existing, nil
}

func (r *lifecycleRepository) GetByID(ctx context.Context, id uuid.UUID) (model.TenantLifecycleRequest, error) {
	return scanLifecycleRequest(r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, action, requested_by, COALESCE(approved_by,''), reason, status, created_at, decided_at
		FROM merchant_tenant_lifecycle_requests WHERE id = $1`, id))
}

func (r *lifecycleRepository) GetPending(ctx context.Context, tenantID uuid.UUID, action string) (model.TenantLifecycleRequest, bool, error) {
	req, err := scanLifecycleRequest(r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, action, requested_by, COALESCE(approved_by,''), reason, status, created_at, decided_at
		FROM merchant_tenant_lifecycle_requests WHERE tenant_id = $1 AND action = $2 AND status = 'pending'`, tenantID, action))
	if errors.Is(err, ErrNotFound) {
		return model.TenantLifecycleRequest{}, false, nil
	}
	if err != nil {
		return model.TenantLifecycleRequest{}, false, err
	}
	return req, true, nil
}

func (r *lifecycleRepository) List(ctx context.Context, tenantID uuid.UUID, status string, limit int) ([]model.TenantLifecycleRequest, error) {
	var rows *sql.Rows
	var err error
	switch status {
	case "":
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, tenant_id, action, requested_by, COALESCE(approved_by,''), reason, status, created_at, decided_at
			FROM merchant_tenant_lifecycle_requests WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2`, tenantID, limit)
	default:
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, tenant_id, action, requested_by, COALESCE(approved_by,''), reason, status, created_at, decided_at
			FROM merchant_tenant_lifecycle_requests WHERE tenant_id = $1 AND status = $2 ORDER BY created_at DESC LIMIT $3`, tenantID, status, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("merchant: list tenant lifecycle requests: %w", err)
	}
	defer rows.Close()

	var out []model.TenantLifecycleRequest
	for rows.Next() {
		req, err := scanLifecycleRequestRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

func (r *lifecycleRepository) Decide(ctx context.Context, id uuid.UUID, status, approvedBy string) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_tenant_lifecycle_requests
		SET status = $1, approved_by = $2, decided_at = now()
		WHERE id = $3 AND status = 'pending'`, status, approvedBy, id)
	if err != nil {
		return false, fmt.Errorf("merchant: decide tenant lifecycle request: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func scanLifecycleRequest(row *sql.Row) (model.TenantLifecycleRequest, error) {
	req, err := scanLifecycleFrom(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.TenantLifecycleRequest{}, ErrNotFound
	}
	if err != nil {
		return model.TenantLifecycleRequest{}, fmt.Errorf("merchant: scan tenant lifecycle request: %w", err)
	}
	return req, nil
}

func scanLifecycleRequestRow(rows *sql.Rows) (model.TenantLifecycleRequest, error) {
	req, err := scanLifecycleFrom(rows)
	if err != nil {
		return model.TenantLifecycleRequest{}, fmt.Errorf("merchant: scan tenant lifecycle request: %w", err)
	}
	return req, nil
}

func scanLifecycleFrom(row rowScanner) (model.TenantLifecycleRequest, error) {
	var req model.TenantLifecycleRequest
	var decidedAt sql.NullTime
	err := row.Scan(&req.ID, &req.TenantID, &req.Action, &req.RequestedBy, &req.ApprovedBy, &req.Reason,
		&req.Status, &req.CreatedAt, &decidedAt)
	if err != nil {
		return model.TenantLifecycleRequest{}, err
	}
	if decidedAt.Valid {
		req.DecidedAt = &decidedAt.Time
	}
	return req, nil
}
