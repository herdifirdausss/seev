package repository

//go:generate mockgen -source=sanctions_repository.go -destination=sanctions_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/herdifirdausss/seev/pkg/database"
)

type SanctionsEntry struct {
	ID, Source, NormalizedName, BirthDate, DatasetVersion string
}

type SanctionsRepository interface {
	ReplaceSanctions(context.Context, []SanctionsEntry) error
	MatchSanctions(context.Context, string, string) (bool, error)
}

type sanctionsRepo struct{ db database.DatabaseSQL }

func NewSanctionsRepository(db database.DatabaseSQL) SanctionsRepository {
	return &sanctionsRepo{db: db}
}

func (r *sanctionsRepo) ReplaceSanctions(ctx context.Context, entries []SanctionsEntry) error {
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sanctions_entries`); err != nil {
			return fmt.Errorf("clear sanctions entries: %w", err)
		}
		for _, entry := range entries {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO sanctions_entries (id, source, normalized_name, birth_date, dataset_version)
				VALUES ($1, $2, $3, NULLIF($4, ''), $5)`, entry.ID, entry.Source, entry.NormalizedName, entry.BirthDate, entry.DatasetVersion); err != nil {
				return fmt.Errorf("insert sanctions entry %s: %w", entry.ID, err)
			}
		}
		return nil
	})
}

func (r *sanctionsRepo) MatchSanctions(ctx context.Context, normalizedName, birthDate string) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM sanctions_entries
			WHERE normalized_name = $1 AND (birth_date IS NULL OR birth_date = NULLIF($2, ''))
		)`, normalizedName, birthDate).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("match sanctions entry: %w", err)
	}
	return exists, nil
}
