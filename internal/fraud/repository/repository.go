package repository

//go:generate mockgen -source=repository.go -destination=repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/fraud/model"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

type ScreeningRepository interface {
	InsertEvent(context.Context, model.ScreeningEvent) error
	ListEvents(ctx context.Context, userID, verdict string, limit, offset int) ([]model.ScreeningEvent, error)
}

type RuleModeRepository interface {
	GetRuleMode(context.Context, string) (mode string, updatedBy string, updatedAt time.Time, found bool, err error)
	SetRuleMode(context.Context, string, string, string) error
}

type SanctionsEntry struct {
	ID, Source, NormalizedName, BirthDate, DatasetVersion string
}

type SanctionsRepository interface {
	ReplaceSanctions(context.Context, []SanctionsEntry) error
	MatchSanctions(context.Context, string, string) (bool, error)
}

type screeningRepo struct{ db database.DatabaseSQL }

func NewScreeningRepository(db database.DatabaseSQL) ScreeningRepository {
	return &screeningRepo{db: db}
}

func NewRuleModeRepository(db database.DatabaseSQL) RuleModeRepository {
	return &screeningRepo{db: db}
}

func NewSanctionsRepository(db database.DatabaseSQL) SanctionsRepository {
	return &screeningRepo{db: db}
}

func (r *screeningRepo) ReplaceSanctions(ctx context.Context, entries []SanctionsEntry) error {
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

func (r *screeningRepo) MatchSanctions(ctx context.Context, normalizedName, birthDate string) (bool, error) {
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

func (r *screeningRepo) GetRuleMode(ctx context.Context, rule string) (string, string, time.Time, bool, error) {
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

func (r *screeningRepo) SetRuleMode(ctx context.Context, rule, mode, updatedBy string) error {
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

func (r *screeningRepo) InsertEvent(ctx context.Context, ev model.ScreeningEvent) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO screening_events (id, tx_type, user_id, amount, currency, rule, verdict, reason, request_id, flow, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())`,
		ev.ID, ev.TxType, ev.UserID, ev.Amount.IntPart(), ev.Currency, ev.Rule, ev.Verdict, ev.Reason,
		generalutil.NullString(ev.RequestID), generalutil.NullString(ev.Flow),
	)
	if err != nil {
		return fmt.Errorf("insert screening event: %w", err)
	}
	return nil
}

func (r *screeningRepo) ListEvents(ctx context.Context, userID, verdict string, limit, offset int) ([]model.ScreeningEvent, error) {
	query := `SELECT id, tx_type, user_id, amount, currency, rule, verdict, reason,
	                 COALESCE(request_id, ''), COALESCE(flow, ''), created_at
	          FROM screening_events WHERE 1=1`
	args := []any{}
	if userID != "" {
		args = append(args, userID)
		query += fmt.Sprintf(" AND user_id = $%d", len(args))
	}
	if verdict != "" {
		args = append(args, verdict)
		query += fmt.Sprintf(" AND verdict = $%d", len(args))
	}
	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list screening events: %w", err)
	}
	defer rows.Close()

	var out []model.ScreeningEvent
	for rows.Next() {
		var ev model.ScreeningEvent
		var amount int64
		if err := rows.Scan(&ev.ID, &ev.TxType, &ev.UserID, &amount, &ev.Currency, &ev.Rule, &ev.Verdict, &ev.Reason,
			&ev.RequestID, &ev.Flow, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan screening event: %w", err)
		}
		ev.Amount = decimal.NewFromInt(amount)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate screening events: %w", err)
	}
	return out, nil
}
