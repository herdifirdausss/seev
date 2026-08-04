package processors

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
)

type currencyAccountRepository interface {
	GetAccountIDByCurrency(ctx context.Context, userID uuid.UUID, accountType, currency string) (uuid.UUID, error)
	GetPocketAccountIDByCurrency(ctx context.Context, userID uuid.UUID, pocketCode, currency string) (uuid.UUID, error)
}

type currencyMerchantAccountRepository interface {
	GetMerchantAccountIDByCurrency(ctx context.Context, tenantID uuid.UUID, accountType, currency string) (uuid.UUID, error)
}

func userAccountID(ctx context.Context, repo repository.AccountRepository, userID uuid.UUID, accountType, currency string) (uuid.UUID, error) {
	if currency != "" {
		if resolver, ok := repo.(currencyAccountRepository); ok {
			return resolver.GetAccountIDByCurrency(ctx, userID, accountType, currency)
		}
		return uuid.Nil, fmt.Errorf("%w: currency-aware user account resolver is unavailable", apperror.ErrCurrencyAccountMissing)
	}
	return repo.GetAccountID(ctx, userID, accountType)
}

func userPocketAccountID(ctx context.Context, repo repository.AccountRepository, userID uuid.UUID, pocketCode, currency string) (uuid.UUID, error) {
	if currency != "" {
		if resolver, ok := repo.(currencyAccountRepository); ok {
			return resolver.GetPocketAccountIDByCurrency(ctx, userID, pocketCode, currency)
		}
		return uuid.Nil, fmt.Errorf("%w: currency-aware pocket account resolver is unavailable", apperror.ErrCurrencyAccountMissing)
	}
	return repo.GetPocketAccountID(ctx, userID, pocketCode)
}

func merchantAccountID(ctx context.Context, repo repository.AccountRepository, tenantID uuid.UUID, accountType, currency string) (uuid.UUID, error) {
	if currency != "" {
		if resolver, ok := repo.(currencyMerchantAccountRepository); ok {
			return resolver.GetMerchantAccountIDByCurrency(ctx, tenantID, accountType, currency)
		}
		return uuid.Nil, fmt.Errorf("%w: currency-aware merchant account resolver is unavailable", apperror.ErrCurrencyAccountMissing)
	}
	return repo.GetMerchantAccountID(ctx, tenantID, accountType)
}
