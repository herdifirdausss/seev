// Package provision creates the standard set of accounts a user needs before
// any ledger transaction can reference them (docs/plan/05 Task 1b.2).
package provision

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	currencyreg "github.com/herdifirdausss/seev/pkg/currency"
)

// DatabaseSQL is the thin interface over the connection pool this service
// needs — mirrors service/handle's own narrow redefinition rather than
// depending on pkg/database directly.
type DatabaseSQL interface {
	WithTx(ctx context.Context, opts *sql.TxOptions, fn func(tx *sql.Tx) error) error
}

// standardAccountTypes are provisioned for every new user. Pocket accounts
// are created on demand via CreatePocket, not here.
var standardAccountTypes = []string{
	constant.AccountTypeCash,
	constant.AccountTypeHold,
	constant.AccountTypePending,
	constant.AccountTypeFrozen,
}

var pocketCodePattern = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

type Service struct {
	db DatabaseSQL
}

func New(db DatabaseSQL) *Service {
	return &Service{db: db}
}

// CreateUserAccounts provisions the standard account set (cash, hold,
// pending, frozen) for a user. Idempotent: calling it again for the same
// user returns the existing accounts without error or duplication.
func (s *Service) CreateUserAccounts(ctx context.Context, userID uuid.UUID, currency string) ([]model.Account, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("%w: userID is required", apperror.ErrValidation)
	}
	if !currencyreg.IsValid(currency) {
		return nil, fmt.Errorf("%w: unsupported currency %q", apperror.ErrValidation, currency)
	}

	accounts := make([]model.Account, 0, len(standardAccountTypes))

	err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		for _, accType := range standardAccountTypes {
			acc, err := upsertAccount(ctx, tx, userID, accType, currency, "")
			if err != nil {
				return fmt.Errorf("provision %s account: %w", accType, err)
			}
			accounts = append(accounts, acc)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

// CreatePocket provisions a single named pocket sub-account for a user.
// Idempotent: calling it again with the same pocket_code returns the
// existing pocket account without error or duplication.
func (s *Service) CreatePocket(ctx context.Context, userID uuid.UUID, currency, pocketCode string) (model.Account, error) {
	if userID == uuid.Nil {
		return model.Account{}, fmt.Errorf("%w: userID is required", apperror.ErrValidation)
	}
	if !currencyreg.IsValid(currency) {
		return model.Account{}, fmt.Errorf("%w: unsupported currency %q", apperror.ErrValidation, currency)
	}
	if !pocketCodePattern.MatchString(pocketCode) {
		return model.Account{}, fmt.Errorf("%w: pocket_code must match %s, got %q", apperror.ErrValidation, pocketCodePattern.String(), pocketCode)
	}

	var acc model.Account
	err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var err error
		acc, err = upsertAccount(ctx, tx, userID, constant.AccountTypePocket, currency, pocketCode)
		return err
	})
	if err != nil {
		return model.Account{}, err
	}
	return acc, nil
}

// upsertAccount creates an account + zero-balance row, or returns the
// existing one if it already exists (ON CONFLICT DO UPDATE is a no-op write
// used only to make RETURNING report the existing row too).
func upsertAccount(ctx context.Context, tx *sql.Tx, userID uuid.UUID, accType, currency, pocketCode string) (model.Account, error) {
	id := uuid.New()

	var conflictTarget string
	var pocketArg any
	if pocketCode == "" {
		conflictTarget = "(owner_type, owner_id, type, currency) WHERE pocket_code IS NULL AND owner_id IS NOT NULL"
		pocketArg = nil
	} else {
		conflictTarget = "(owner_type, owner_id, type, currency, pocket_code) WHERE pocket_code IS NOT NULL"
		pocketArg = pocketCode
	}

	var acc model.Account
	acc.OwnerID = userID
	acc.Type = accType
	acc.Currency = currency
	acc.PocketCode = pocketCode

	query := fmt.Sprintf(`
		INSERT INTO accounts (id, owner_id, owner_type, type, currency, pocket_code, status, created_by)
		VALUES ($1, $2, 'user', $3, $4, $5, 'active', 'service:ledger-provision')
		ON CONFLICT %s DO UPDATE SET updated_at = accounts.updated_at
		RETURNING id, status`, conflictTarget)

	if err := tx.QueryRowContext(ctx, query, id, userID, accType, currency, pocketArg).Scan(&acc.ID, &acc.Status); err != nil {
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
