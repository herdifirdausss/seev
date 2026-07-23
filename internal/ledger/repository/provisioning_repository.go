package repository

//go:generate mockgen -source=provisioning_repository.go -destination=provisioning_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/model"
)

// UpsertAccountParams groups the fields needed to provision (or
// idempotently re-fetch) one account row plus its zero-balance companion.
type UpsertAccountParams struct {
	OwnerID    uuid.UUID
	Type       string
	Currency   string
	PocketCode string // empty for non-pocket accounts
	CreatedBy  string
}

// ProvisioningRepository abstracts account creation — deliberately
// SEPARATE from AccountRepository (which is documented as read-only,
// outside any ledger transaction): provisioning is a write that must run
// inside the caller's own transaction, a different contract entirely.
type ProvisioningRepository interface {
	// UpsertAccount creates an account + zero-balance row, or returns the
	// existing one if it already exists (docs/roadmap/archive/05 Task 1b.2) —
	// ON CONFLICT DO UPDATE is a no-op write used only to make RETURNING
	// report the existing row too.
	UpsertAccount(ctx context.Context, tx *sql.Tx, params UpsertAccountParams) (model.Account, error)
}

type provisioningRepo struct{}

// NewProvisioningRepository constructs a ProvisioningRepository. It holds
// no state of its own — every method receives the caller's own *sql.Tx —
// so a single package-level instance is fine to share across callers.
func NewProvisioningRepository() ProvisioningRepository {
	return &provisioningRepo{}
}

func (r *provisioningRepo) UpsertAccount(ctx context.Context, tx *sql.Tx, p UpsertAccountParams) (model.Account, error) {
	id := uuid.New()

	var conflictTarget string
	var pocketArg any
	if p.PocketCode == "" {
		conflictTarget = "(owner_type, owner_id, type, currency) WHERE pocket_code IS NULL AND owner_id IS NOT NULL"
		pocketArg = nil
	} else {
		conflictTarget = "(owner_type, owner_id, type, currency, pocket_code) WHERE pocket_code IS NOT NULL"
		pocketArg = p.PocketCode
	}

	var acc model.Account
	acc.OwnerID = p.OwnerID
	acc.Type = p.Type
	acc.Currency = p.Currency
	acc.PocketCode = p.PocketCode

	query := fmt.Sprintf(`
		INSERT INTO accounts (id, owner_id, owner_type, type, currency, pocket_code, status, created_by)
		VALUES ($1, $2, 'user', $3, $4, $5, 'active', $6)
		ON CONFLICT %s DO UPDATE SET updated_at = accounts.updated_at
		RETURNING id, status`, conflictTarget)

	if err := tx.QueryRowContext(ctx, query, id, p.OwnerID, p.Type, p.Currency, pocketArg, p.CreatedBy).Scan(&acc.ID, &acc.Status); err != nil {
		return model.Account{}, fmt.Errorf("upsert account: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO account_balances (account_id) VALUES ($1) ON CONFLICT (account_id) DO NOTHING`,
		acc.ID,
	); err != nil {
		return model.Account{}, fmt.Errorf("upsert account balance: %w", err)
	}

	return acc, nil
}
