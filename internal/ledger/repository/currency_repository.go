package repository

//go:generate mockgen -source=currency_repository.go -destination=currency_repository_mock.go -package=repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/database"
)

// CurrencyRepository is a small, standalone repository (deliberately not
// folded into AccountRepository, already large) that reads the `currencies`
// table (docs/roadmap/archive/18 Task T1) for internal/ledger.NewModule's startup
// currency.Load call.
type CurrencyRepository interface {
	// ListEnabled returns currencies that are enabled for normal intake.
	ListEnabled(ctx context.Context) ([]currency.Currency, error)
}

type currencyRepo struct {
	db database.DatabaseSQL
}

func NewCurrencyRepository(db database.DatabaseSQL) CurrencyRepository {
	return &currencyRepo{db: db}
}

func (r *currencyRepo) ListEnabled(ctx context.Context) ([]currency.Currency, error) {
	return r.list(ctx, "enabled = true AND status IN ('active', 'intake_paused')", "list enabled currencies")
}

func (r *currencyRepo) ListRegistered(ctx context.Context) ([]currency.Currency, error) {
	return r.list(ctx, "status <> 'draft'", "list registered currencies")
}

func (r *currencyRepo) list(ctx context.Context, predicate, operation string) ([]currency.Currency, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT code, minor_unit, status, operations
		FROM currencies
		WHERE `+predicate+`
		ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	defer rows.Close()

	var out []currency.Currency
	for rows.Next() {
		var c currency.Currency
		var rawOperations []byte
		if err := rows.Scan(&c.Code, &c.MinorUnit, &c.Status, &rawOperations); err != nil {
			return nil, fmt.Errorf("scan currency: %w", err)
		}
		c.Code = strings.TrimSpace(c.Code)
		if len(rawOperations) > 0 {
			if err := json.Unmarshal(rawOperations, &c.Operations); err != nil {
				return nil, fmt.Errorf("decode currency operations: %w", err)
			}
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate currencies: %w", err)
	}
	return out, nil
}
