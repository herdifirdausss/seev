package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/herdifirdausss/seev/pkg/database"
)

type settingsRepository struct {
	db database.DatabaseSQL
}

func (r *settingsRepository) Get(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM merchant_settings WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("merchant: get setting %q: %w", key, err)
	}
	return value, true, nil
}

func (r *settingsRepository) Set(ctx context.Context, key, value, updatedBy string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO merchant_settings (key, value, updated_by, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (key) DO UPDATE SET value = $2, updated_by = $3, updated_at = now()`,
		key, value, updatedBy,
	)
	if err != nil {
		return fmt.Errorf("merchant: set setting %q: %w", key, err)
	}
	return nil
}
