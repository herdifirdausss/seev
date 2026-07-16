package repository

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/fraud/model"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

type ScreeningRepository interface {
	InsertEvent(context.Context, model.ScreeningEvent) error
	ListEvents(ctx context.Context, userID, verdict string, limit, offset int) ([]model.ScreeningEvent, error)
}

type screeningRepo struct{ db database.DatabaseSQL }

func NewScreeningRepository(db database.DatabaseSQL) ScreeningRepository {
	return &screeningRepo{db: db}
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
