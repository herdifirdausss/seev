package repository

//go:generate mockgen -source=rule_mode_repository.go -destination=rule_mode_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/herdifirdausss/seev/pkg/database"
)

type RuleModeRepository interface {
	GetRuleMode(context.Context, string) (mode string, updatedBy string, updatedAt time.Time, found bool, err error)
	SetRuleMode(context.Context, string, string, string) error
}

type ruleModeRepo struct{ db database.DatabaseSQL }

func NewRuleModeRepository(db database.DatabaseSQL) RuleModeRepository {
	return &ruleModeRepo{db: db}
}

func (r *ruleModeRepo) GetRuleMode(ctx context.Context, rule string) (string, string, time.Time, bool, error) {
	var mode, updatedBy string
	var updatedAt time.Time
	err := r.db.QueryRowContext(ctx, `
		SELECT mode, updated_by, updated_at
		FROM screening_rule_modes WHERE rule = $1`, rule).Scan(&mode, &updatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", time.Time{}, false, nil
	}
	if err != nil {
		return "", "", time.Time{}, false, fmt.Errorf("get screening rule mode: %w", err)
	}
	return mode, updatedBy, updatedAt, true, nil
}

func (r *ruleModeRepo) SetRuleMode(ctx context.Context, rule, mode, updatedBy string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO screening_rule_modes (rule, mode, updated_by, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (rule) DO UPDATE SET mode = EXCLUDED.mode, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		rule, mode, updatedBy)
	if err != nil {
		return fmt.Errorf("set screening rule mode: %w", err)
	}
	return nil
}
