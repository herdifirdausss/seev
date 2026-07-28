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

type apiKeyRepository struct {
	db database.DatabaseSQL
}

func (r *apiKeyRepository) Create(ctx context.Context, k model.APIKey) error {
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO merchant_api_keys
				(id, public_id, tenant_id, public_prefix, secret_digest, environment, status, expires_at, created_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())`,
			k.ID, k.PublicID, k.TenantID, k.PublicPrefix, k.SecretDigest, k.Environment, k.Status, k.ExpiresAt, k.CreatedBy,
		)
		if err != nil {
			return fmt.Errorf("merchant: create api key: %w", err)
		}
		for _, scope := range k.Scopes {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO merchant_api_key_scopes (key_id, scope) VALUES ($1, $2)`, k.ID, scope); err != nil {
				return fmt.Errorf("merchant: create api key scope %q: %w", scope, err)
			}
		}
		return nil
	})
}

func (r *apiKeyRepository) GetActiveByPrefix(ctx context.Context, prefix string) (model.APIKey, error) {
	k, err := r.scanKey(r.db.QueryRowContext(ctx, `
		SELECT id, public_id, tenant_id, public_prefix, secret_digest, environment, status, expires_at,
		       last_used_at, created_by, revoked_by, created_at, revoked_at
		FROM merchant_api_keys WHERE public_prefix = $1 AND status = 'active'`, prefix))
	if err != nil {
		return model.APIKey{}, err
	}
	scopes, err := r.scopesFor(ctx, k.ID)
	if err != nil {
		return model.APIKey{}, err
	}
	k.Scopes = scopes
	return k, nil
}

func (r *apiKeyRepository) ListByTenant(ctx context.Context, tenantID uuid.UUID) ([]model.APIKey, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, public_id, tenant_id, public_prefix, secret_digest, environment, status, expires_at,
		       last_used_at, created_by, revoked_by, created_at, revoked_at
		FROM merchant_api_keys WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("merchant: list api keys: %w", err)
	}
	defer rows.Close()

	var out []model.APIKey
	for rows.Next() {
		var k model.APIKey
		if err := rows.Scan(&k.ID, &k.PublicID, &k.TenantID, &k.PublicPrefix, &k.SecretDigest, &k.Environment,
			&k.Status, &k.ExpiresAt, &k.LastUsedAt, &k.CreatedBy, &k.RevokedBy, &k.CreatedAt, &k.RevokedAt); err != nil {
			return nil, fmt.Errorf("merchant: scan api key: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Revoke is tenant-scoped (§7.3) — WHERE tenant_id = $1 AND id = $2, so an
// attempt to revoke a different tenant's key affects zero rows rather than
// silently succeeding cross-tenant.
func (r *apiKeyRepository) Revoke(ctx context.Context, tenantID, keyID uuid.UUID, actor string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_api_keys SET status = 'revoked', revoked_by = $1, revoked_at = now()
		WHERE tenant_id = $2 AND id = $3 AND status = 'active'`, actor, tenantID, keyID)
	if err != nil {
		return fmt.Errorf("merchant: revoke api key: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchLastUsed is deliberately NOT tenant-scoped by an external caller —
// it runs on the authenticated request's OWN key (already resolved and
// validated for that tenant upstream in T3's auth flow), and per §8.5 must
// be called at most once per configured sampling interval, never on every
// request.
func (r *apiKeyRepository) TouchLastUsed(ctx context.Context, keyID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE merchant_api_keys SET last_used_at = now() WHERE id = $1`, keyID)
	if err != nil {
		return fmt.Errorf("merchant: touch api key last_used_at: %w", err)
	}
	return nil
}

func (r *apiKeyRepository) scopesFor(ctx context.Context, keyID uuid.UUID) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT scope FROM merchant_api_key_scopes WHERE key_id = $1 ORDER BY scope`, keyID)
	if err != nil {
		return nil, fmt.Errorf("merchant: list api key scopes: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			return nil, fmt.Errorf("merchant: scan api key scope: %w", err)
		}
		out = append(out, scope)
	}
	return out, rows.Err()
}

func (r *apiKeyRepository) scanKey(row *sql.Row) (model.APIKey, error) {
	var k model.APIKey
	err := row.Scan(&k.ID, &k.PublicID, &k.TenantID, &k.PublicPrefix, &k.SecretDigest, &k.Environment, &k.Status,
		&k.ExpiresAt, &k.LastUsedAt, &k.CreatedBy, &k.RevokedBy, &k.CreatedAt, &k.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.APIKey{}, ErrNotFound
	}
	if err != nil {
		return model.APIKey{}, fmt.Errorf("merchant: scan api key: %w", err)
	}
	return k, nil
}
