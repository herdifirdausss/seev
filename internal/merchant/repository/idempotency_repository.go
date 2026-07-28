package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

type idempotencyRepository struct {
	db database.DatabaseSQL
}

// Claim attempts to insert a new "processing" record. claimed=false means a
// record for (tenant_id, operation_id, idempotency_key) already existed —
// existing is that row, and the caller (internal/merchant/idempotency, T4)
// decides from its state/request_hash whether that means "replay the
// stored response", "IDEMPOTENCY_KEY_REUSED" (hash differs), or
// "IDEMPOTENCY_IN_PROGRESS" (state is still 'processing'). This method
// only does the atomic claim-or-read; it does not itself apply that
// policy.
func (r *idempotencyRepository) Claim(ctx context.Context, rec model.IdempotencyRecord) (bool, model.IdempotencyRecord, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO merchant_idempotency_records
			(id, tenant_id, operation_id, idempotency_key, request_hash, downstream_key, state,
			 lease_owner, lease_expires_at, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'processing', $7, $8, $9, now(), now())
		ON CONFLICT (tenant_id, operation_id, idempotency_key) DO NOTHING`,
		rec.ID, rec.TenantID, rec.OperationID, rec.IdempotencyKey, rec.RequestHash, rec.DownstreamKey,
		rec.LeaseOwner, rec.LeaseExpiresAt, rec.ExpiresAt,
	)
	if err != nil {
		return false, model.IdempotencyRecord{}, fmt.Errorf("merchant: claim idempotency record: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, model.IdempotencyRecord{}, nil
	}

	existing, err := r.GetByKey(ctx, rec.TenantID, rec.OperationID, rec.IdempotencyKey)
	if err != nil {
		return false, model.IdempotencyRecord{}, err
	}
	return false, existing, nil
}

func (r *idempotencyRepository) Complete(ctx context.Context, tenantID, id uuid.UUID, httpStatus int, responseBody, responseHeaders []byte, resourceID *string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_idempotency_records
		SET state = 'completed', http_status = $1, response_body = $2, response_headers = $3,
		    resource_id = $4, lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE tenant_id = $5 AND id = $6`,
		httpStatus, responseBody, responseHeaders, resourceID, tenantID, id,
	)
	if err != nil {
		return fmt.Errorf("merchant: complete idempotency record: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *idempotencyRepository) Fail(ctx context.Context, tenantID, id uuid.UUID, errorCode string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_idempotency_records
		SET state = 'failed', error_code = $1, lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE tenant_id = $2 AND id = $3`,
		errorCode, tenantID, id,
	)
	if err != nil {
		return fmt.Errorf("merchant: fail idempotency record: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *idempotencyRepository) GetByKey(ctx context.Context, tenantID uuid.UUID, operationID, idempotencyKey string) (model.IdempotencyRecord, error) {
	var rec model.IdempotencyRecord
	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, operation_id, idempotency_key, request_hash, downstream_key, state,
		       resource_id, http_status, response_body, response_headers, error_code,
		       lease_owner, lease_expires_at, expires_at, created_at, updated_at
		FROM merchant_idempotency_records WHERE tenant_id = $1 AND operation_id = $2 AND idempotency_key = $3`,
		tenantID, operationID, idempotencyKey,
	).Scan(&rec.ID, &rec.TenantID, &rec.OperationID, &rec.IdempotencyKey, &rec.RequestHash, &rec.DownstreamKey,
		&rec.State, &rec.ResourceID, &rec.HTTPStatus, &rec.ResponseBody, &rec.ResponseHeaders, &rec.ErrorCode,
		&rec.LeaseOwner, &rec.LeaseExpiresAt, &rec.ExpiresAt, &rec.CreatedAt, &rec.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.IdempotencyRecord{}, ErrNotFound
	}
	if err != nil {
		return model.IdempotencyRecord{}, fmt.Errorf("merchant: get idempotency record: %w", err)
	}
	return rec, nil
}

func (r *idempotencyRepository) TakeoverExpiredLease(ctx context.Context, tenantID, id uuid.UUID, newLeaseOwner string, newLeaseExpiresAt time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_idempotency_records
		SET lease_owner = $1, lease_expires_at = $2, updated_at = now()
		WHERE tenant_id = $3 AND id = $4 AND state = 'processing' AND lease_expires_at < now()`,
		newLeaseOwner, newLeaseExpiresAt, tenantID, id,
	)
	if err != nil {
		return false, fmt.Errorf("merchant: takeover expired idempotency lease: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (r *idempotencyRepository) ReclaimFailed(ctx context.Context, tenantID, id uuid.UUID, newLeaseOwner string, newLeaseExpiresAt time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_idempotency_records
		SET state = 'processing', lease_owner = $1, lease_expires_at = $2, error_code = NULL, updated_at = now()
		WHERE tenant_id = $3 AND id = $4 AND state = 'failed'`,
		newLeaseOwner, newLeaseExpiresAt, tenantID, id,
	)
	if err != nil {
		return false, fmt.Errorf("merchant: reclaim failed idempotency record: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
