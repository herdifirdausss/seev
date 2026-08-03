// Package provision creates the standard set of accounts a user needs before
// any ledger transaction can reference them (docs/roadmap/archive/05 Task 1b.2).
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
	"github.com/herdifirdausss/seev/internal/ledger/repository"
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
	db   DatabaseSQL
	repo repository.ProvisioningRepository
}

func New(db DatabaseSQL, repo repository.ProvisioningRepository) *Service {
	return &Service{db: db, repo: repo}
}

// CreateUserAccounts provisions the standard account set (cash, hold,
// pending, frozen) for a user. Idempotent: calling it again for the same
// user returns the existing accounts without error or duplication.
func (s *Service) CreateUserAccounts(ctx context.Context, userID uuid.UUID, currency string) ([]model.Account, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("%w: userID is required", apperror.ErrValidation)
	}
	if !currencyreg.IsValid(currency) {
		return nil, fmt.Errorf("%w: unsupported currency %q", apperror.ErrCurrencyInvalid, currency)
	}
	if !currencyreg.IsEnabled(currency) {
		return nil, fmt.Errorf("%w: currency %q is not active", apperror.ErrCurrencyDisabled, currency)
	}
	if !currencyreg.Allows(currency, "account_enable") {
		return nil, fmt.Errorf("%w: account_enable is disabled for %q", apperror.ErrCurrencyOperationDisabled, currency)
	}

	accounts := make([]model.Account, 0, len(standardAccountTypes))

	err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		for _, accType := range standardAccountTypes {
			acc, err := s.repo.UpsertAccount(ctx, tx, repository.UpsertAccountParams{
				OwnerID: userID, Type: accType, Currency: currency, CreatedBy: "service:ledger-provision",
			})
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
		return model.Account{}, fmt.Errorf("%w: unsupported currency %q", apperror.ErrCurrencyInvalid, currency)
	}
	if !currencyreg.IsEnabled(currency) {
		return model.Account{}, fmt.Errorf("%w: currency %q is not active", apperror.ErrCurrencyDisabled, currency)
	}
	if !currencyreg.Allows(currency, "account_enable") {
		return model.Account{}, fmt.Errorf("%w: account_enable is disabled for %q", apperror.ErrCurrencyOperationDisabled, currency)
	}
	if !pocketCodePattern.MatchString(pocketCode) {
		return model.Account{}, fmt.Errorf("%w: pocket_code must match %s, got %q", apperror.ErrValidation, pocketCodePattern.String(), pocketCode)
	}

	var acc model.Account
	err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var err error
		acc, err = s.repo.UpsertAccount(ctx, tx, repository.UpsertAccountParams{
			OwnerID: userID, Type: constant.AccountTypePocket, Currency: currency,
			PocketCode: pocketCode, CreatedBy: "service:ledger-provision",
		})
		return err
	})
	if err != nil {
		return model.Account{}, err
	}
	return acc, nil
}

// ProvisionMerchantAccount creates the owner_type='merchant' cash account a
// tenant needs before any merchant transfer or pay-in can reference it
// (Plan 57 T5). Idempotent: calling it again for the same tenant returns
// the existing account without error or duplication.
func (s *Service) ProvisionMerchantAccount(ctx context.Context, tenantID uuid.UUID, currency string) (model.Account, error) {
	return s.provisionMerchantAccountType(ctx, tenantID, currency, constant.AccountTypeCash)
}

// ProvisionMerchantHoldAccount creates the owner_type='merchant' hold
// account a tenant needs before any merchant payout can reference it
// (Plan 57 T6) — merchant_payout_hold/settle/cancel need the same
// cash↔hold state machine WithdrawInitiate/Settle/Cancel already use for
// end users. A merchant does NOT get pending/frozen accounts — those
// remain end-user-only withdrawal-lifecycle states with no merchant
// equivalent. Idempotent, same shape as ProvisionMerchantAccount.
func (s *Service) ProvisionMerchantHoldAccount(ctx context.Context, tenantID uuid.UUID, currency string) (model.Account, error) {
	return s.provisionMerchantAccountType(ctx, tenantID, currency, constant.AccountTypeHold)
}

func (s *Service) provisionMerchantAccountType(ctx context.Context, tenantID uuid.UUID, currency, accountType string) (model.Account, error) {
	if tenantID == uuid.Nil {
		return model.Account{}, fmt.Errorf("%w: tenantID is required", apperror.ErrValidation)
	}
	if !currencyreg.IsValid(currency) {
		return model.Account{}, fmt.Errorf("%w: unsupported currency %q", apperror.ErrCurrencyInvalid, currency)
	}
	if !currencyreg.IsEnabled(currency) {
		return model.Account{}, fmt.Errorf("%w: currency %q is not active", apperror.ErrCurrencyDisabled, currency)
	}
	if !currencyreg.Allows(currency, "account_enable") {
		return model.Account{}, fmt.Errorf("%w: account_enable is disabled for %q", apperror.ErrCurrencyOperationDisabled, currency)
	}

	var acc model.Account
	err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var err error
		acc, err = s.repo.UpsertMerchantAccount(ctx, tx, repository.UpsertMerchantAccountParams{
			TenantID: tenantID, Type: accountType, Currency: currency, CreatedBy: "service:ledger-provision",
		})
		return err
	})
	if err != nil {
		return model.Account{}, err
	}
	return acc, nil
}
