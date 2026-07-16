package repository

//go:generate mockgen -source=account_repository.go -destination=account_repository_mock.go -package=repository
import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

// =============================================================================
// AccountRepository
// =============================================================================

// AccountRepository abstracts all account lookups.
// Implementations use a read-only DB connection (outside any ledger transaction).
type AccountRepository interface {
	// GetAccountID returns the account_id for a user's account of a given type.
	GetAccountID(ctx context.Context, userID uuid.UUID, accountType string) (uuid.UUID, error)

	// GetPocketAccountID returns a user's pocket account for a given pocket_code.
	GetPocketAccountID(ctx context.Context, userID uuid.UUID, pocketCode string) (uuid.UUID, error)

	// GetAccountCurrency returns the currency of an account.
	GetAccountCurrency(ctx context.Context, accountID uuid.UUID) (string, error)

	// GetSystemAccountID returns a platform-level system account (settlement,
	// fee, escrow, chargeback, adjustment, confiscated). qualifier is the
	// shard key documented in processors.go (gateway for settlement/fee, card
	// network for chargeback, currency for escrow, "" for adjustment/confiscated).
	// currency filters to the account pool for that currency (docs/plan/18
	// Task T2) — a qualifier like "bca" now names a FAMILY of accounts, one
	// per currency, not a single account; currency picks the member.
	GetSystemAccountID(ctx context.Context, accountType string, qualifier string, currency string) (uuid.UUID, error)

	// ListByOwner returns all accounts owned by a user, for read APIs
	// (GET /accounts).
	ListByOwner(ctx context.Context, userID uuid.UUID) ([]model.Account, error)

	// GetOwnerID returns the owner_id of an account, for ownership checks in
	// read APIs (e.g. does this transaction/account belong to the caller).
	GetOwnerID(ctx context.Context, accountID uuid.UUID) (uuid.UUID, error)
}

type accountRepo struct {
	db database.DatabaseSQL

	// Caches for docs/plan/11 Task T3 — every one of these is positive-only
	// (a "not found" result is never cached, so a not-yet-provisioned
	// pocket or a genuinely missing account is always re-checked against
	// the DB) and has no TTL/eviction: the values themselves are immutable
	// once they exist (an account's id, currency, and a system account's
	// identity for a given type+qualifier never change after creation).
	//
	// LIMITATION: unbounded growth — at very large scale (millions of
	// distinct accounts touched over the process lifetime) this becomes a
	// real memory concern. Add LRU eviction (e.g. hashicorp/golang-lru)
	// before scaling past roughly 1M distinct accounts touched; not needed
	// for the deployment scale this MVP targets.
	systemAccountCache sync.Map // key: "type:qualifier" -> uuid.UUID
	userAccountCache   sync.Map // key: "ownerID:type:pocketCode" -> uuid.UUID
	currencyCache      sync.Map // key: accountID -> currency string
}

// NewAccountRepository requires a DB handle for read-only lookups outside
// any ledger posting transaction (see ledger.Service / repository doc above).
func NewAccountRepository(db database.DatabaseSQL) AccountRepository {
	return &accountRepo{db: db}
}

func (r *accountRepo) GetAccountID(ctx context.Context, userID uuid.UUID, accountType string) (uuid.UUID, error) {
	key := userID.String() + ":" + accountType + ":"
	if v, ok := r.userAccountCache.Load(key); ok {
		return v.(uuid.UUID), nil
	}

	var id uuid.UUID
	err := r.db.QueryRowContext(ctx, `
		SELECT id FROM accounts
		WHERE owner_type = 'user' AND owner_id = $1 AND type = $2
		  AND pocket_code IS NULL AND status = 'active'`,
		userID, accountType,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("%w: user %s type %s", apperror.ErrAccountNotFound, userID, accountType)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("get account id: %w", err)
	}
	r.userAccountCache.Store(key, id)
	return id, nil
}

func (r *accountRepo) GetPocketAccountID(ctx context.Context, userID uuid.UUID, pocketCode string) (uuid.UUID, error) {
	key := userID.String() + ":pocket:" + pocketCode
	if v, ok := r.userAccountCache.Load(key); ok {
		return v.(uuid.UUID), nil
	}

	var id uuid.UUID
	err := r.db.QueryRowContext(ctx, `
		SELECT id FROM accounts
		WHERE owner_type = 'user' AND owner_id = $1 AND type = 'pocket'
		  AND pocket_code = $2 AND status = 'active'`,
		userID, pocketCode,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("%w: user %s pocket %q", apperror.ErrAccountNotFound, userID, pocketCode)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("get pocket account id: %w", err)
	}
	r.userAccountCache.Store(key, id)
	return id, nil
}

func (r *accountRepo) GetAccountCurrency(ctx context.Context, accountID uuid.UUID) (string, error) {
	if v, ok := r.currencyCache.Load(accountID); ok {
		return v.(string), nil
	}

	var currency string
	err := r.db.QueryRowContext(ctx, `SELECT currency FROM accounts WHERE id = $1`, accountID).Scan(&currency)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: %s", apperror.ErrAccountNotFound, accountID)
	}
	if err != nil {
		return "", fmt.Errorf("get account currency: %w", err)
	}
	r.currencyCache.Store(accountID, currency)
	return currency, nil
}

func (r *accountRepo) GetSystemAccountID(ctx context.Context, accountType string, qualifier string, currency string) (uuid.UUID, error) {
	key := accountType + ":" + qualifier + ":" + currency
	if v, ok := r.systemAccountCache.Load(key); ok {
		return v.(uuid.UUID), nil
	}

	var id uuid.UUID
	err := r.db.QueryRowContext(ctx, `
		SELECT id FROM accounts
		WHERE owner_type = 'system' AND type = $1 AND COALESCE(system_qualifier, '') = $2 AND currency = $3`,
		accountType, qualifier, currency,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("%w: system account type %s qualifier %q currency %q", apperror.ErrAccountNotFound, accountType, qualifier, currency)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("get system account id: %w", err)
	}
	r.systemAccountCache.Store(key, id)
	return id, nil
}

func (r *accountRepo) ListByOwner(ctx context.Context, userID uuid.UUID) ([]model.Account, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, owner_id, type, currency, COALESCE(pocket_code, ''), status
		FROM accounts
		WHERE owner_type = 'user' AND owner_id = $1
		ORDER BY type, pocket_code`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list accounts by owner: %w", err)
	}
	defer rows.Close()

	accounts := make([]model.Account, 0)
	for rows.Next() {
		var a model.Account
		if err := rows.Scan(&a.ID, &a.OwnerID, &a.Type, &a.Currency, &a.PocketCode, &a.Status); err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accounts: %w", err)
	}
	return accounts, nil
}

func (r *accountRepo) GetOwnerID(ctx context.Context, accountID uuid.UUID) (uuid.UUID, error) {
	var ownerID uuid.NullUUID
	err := r.db.QueryRowContext(ctx, `SELECT owner_id FROM accounts WHERE id = $1`, accountID).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("%w: %s", apperror.ErrAccountNotFound, accountID)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("get owner id: %w", err)
	}
	return ownerID.UUID, nil
}
