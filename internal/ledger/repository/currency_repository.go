package repository

//go:generate mockgen -source=currency_repository.go -destination=currency_repository_mock.go -package=repository

import (
	"context"
	"fmt"

	"github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/database"
)

// CurrencyRepository is a small, standalone repository (deliberately not
// folded into AccountRepository, already large) that reads the `currencies`
// table (docs/plan/18 Task T1) for internal/ledger.NewModule's startup
// currency.Load call.
type CurrencyRepository interface {
	// ListEnabled returns every currency with enabled=true.
	ListEnabled(ctx context.Context) ([]currency.Currency, error)
}

type currencyRepo struct {
	db database.DatabaseSQL
}

func NewCurrencyRepository(db database.DatabaseSQL) CurrencyRepository {
	return &currencyRepo{db: db}
}

func (r *currencyRepo) ListEnabled(ctx context.Context) ([]currency.Currency, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT code, minor_unit FROM currencies WHERE enabled = true`)
	if err != nil {
		return nil, fmt.Errorf("list enabled currencies: %w", err)
	}
	defer rows.Close()

	var out []currency.Currency
	for rows.Next() {
		var c currency.Currency
		if err := rows.Scan(&c.Code, &c.MinorUnit); err != nil {
			return nil, fmt.Errorf("scan currency: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate currencies: %w", err)
	}
	return out, nil
}
