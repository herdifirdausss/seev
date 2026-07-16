package repository

//go:generate mockgen -source=savings_repository.go -destination=savings_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

// SavingsRepository persists which accounts earn interest and at what
// rate (docs/plan/19 Task T3).
type SavingsRepository interface {
	// Upsert inserts or updates one account's config — ops re-registering
	// an already-configured account changes its rate/enabled in place.
	Upsert(ctx context.Context, tx *sql.Tx, cfg model.SavingsConfig) error

	// Get is a read-only lookup outside any transaction.
	Get(ctx context.Context, accountID uuid.UUID) (model.SavingsConfig, error)

	// ListEnabled returns every enabled=true row — the daily accrual job's
	// own iteration set.
	ListEnabled(ctx context.Context) ([]model.SavingsConfig, error)

	// ListAll returns every row (enabled or not) — GET /admin/savings.
	ListAll(ctx context.Context) ([]model.SavingsConfig, error)
}

type savingsRepo struct {
	db database.DatabaseSQL
}

func NewSavingsRepository(db database.DatabaseSQL) SavingsRepository {
	return &savingsRepo{db: db}
}

func (r *savingsRepo) Upsert(ctx context.Context, tx *sql.Tx, cfg model.SavingsConfig) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO savings_config (account_id, annual_rate_bps, enabled, created_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (account_id) DO UPDATE SET annual_rate_bps = $2, enabled = $3`,
		cfg.AccountID, cfg.AnnualRateBps, cfg.Enabled,
	)
	if err != nil {
		return fmt.Errorf("upsert savings config: %w", err)
	}
	return nil
}

func (r *savingsRepo) Get(ctx context.Context, accountID uuid.UUID) (model.SavingsConfig, error) {
	var cfg model.SavingsConfig
	err := r.db.QueryRowContext(ctx, `
		SELECT account_id, annual_rate_bps, enabled, created_at, updated_at
		FROM savings_config WHERE account_id = $1`, accountID,
	).Scan(&cfg.AccountID, &cfg.AnnualRateBps, &cfg.Enabled, &cfg.CreatedAt, &cfg.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.SavingsConfig{}, fmt.Errorf("%w: %s", apperror.ErrSavingsConfigNotFound, accountID)
	}
	if err != nil {
		return model.SavingsConfig{}, fmt.Errorf("get savings config: %w", err)
	}
	return cfg, nil
}

func (r *savingsRepo) ListEnabled(ctx context.Context) ([]model.SavingsConfig, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT account_id, annual_rate_bps, enabled, created_at, updated_at
		FROM savings_config WHERE enabled = true ORDER BY account_id`)
	if err != nil {
		return nil, fmt.Errorf("list enabled savings config: %w", err)
	}
	defer rows.Close()
	return scanSavingsConfigRows(rows)
}

func (r *savingsRepo) ListAll(ctx context.Context) ([]model.SavingsConfig, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT account_id, annual_rate_bps, enabled, created_at, updated_at
		FROM savings_config ORDER BY account_id`)
	if err != nil {
		return nil, fmt.Errorf("list savings config: %w", err)
	}
	defer rows.Close()
	return scanSavingsConfigRows(rows)
}

func scanSavingsConfigRows(rows *sql.Rows) ([]model.SavingsConfig, error) {
	var out []model.SavingsConfig
	for rows.Next() {
		var cfg model.SavingsConfig
		if err := rows.Scan(&cfg.AccountID, &cfg.AnnualRateBps, &cfg.Enabled, &cfg.CreatedAt, &cfg.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan savings config: %w", err)
		}
		out = append(out, cfg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate savings config: %w", err)
	}
	return out, nil
}
