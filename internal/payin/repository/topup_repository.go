package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

func (r *repo) InsertTopupIntent(ctx context.Context, intent model.TopupIntent) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO payin_topup_intents
			(id, reference, user_id, amount, currency, vendor, status, expires_at, request_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8, now(), now())`,
		intent.ID, intent.Reference, intent.UserID, intent.Amount.IntPart(), intent.Currency, intent.Vendor, intent.ExpiresAt,
		generalutil.NullString(intent.RequestID),
	)
	if err != nil {
		return fmt.Errorf("insert payin topup intent: %w", err)
	}
	return nil
}

func (r *repo) GetTopupIntent(ctx context.Context, id uuid.UUID) (model.TopupIntent, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, reference, user_id, amount, currency, vendor, status, settled_event_id, expires_at,
		       COALESCE(request_id, ''), created_at, updated_at
		FROM payin_topup_intents WHERE id = $1`, id)
	intent, err := scanTopupIntent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.TopupIntent{}, ErrNotFound
	}
	return intent, err
}

func (r *repo) GetTopupIntentByReference(ctx context.Context, reference string) (model.TopupIntent, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, reference, user_id, amount, currency, vendor, status, settled_event_id, expires_at,
		       COALESCE(request_id, ''), created_at, updated_at
		FROM payin_topup_intents WHERE reference = $1`, reference)
	intent, err := scanTopupIntent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.TopupIntent{}, false, nil
	}
	if err != nil {
		return model.TopupIntent{}, false, err
	}
	return intent, true, nil
}

func scanTopupIntent(row *sql.Row) (model.TopupIntent, error) {
	var intent model.TopupIntent
	var amount int64
	var settledEventID sql.NullString
	if err := row.Scan(&intent.ID, &intent.Reference, &intent.UserID, &amount, &intent.Currency, &intent.Vendor,
		&intent.Status, &settledEventID, &intent.ExpiresAt, &intent.RequestID, &intent.CreatedAt, &intent.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.TopupIntent{}, err
		}
		return model.TopupIntent{}, fmt.Errorf("scan payin topup intent: %w", err)
	}
	intent.Amount = decimal.NewFromInt(amount)
	if settledEventID.Valid {
		id, err := uuid.Parse(settledEventID.String)
		if err != nil {
			return model.TopupIntent{}, fmt.Errorf("parse settled_event_id: %w", err)
		}
		intent.SettledEventID = &id
	}
	return intent, nil
}

func (r *repo) MarkTopupIntentSettled(ctx context.Context, reference string, eventID uuid.UUID) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE payin_topup_intents SET status = 'settled', settled_event_id = $1, updated_at = now()
		WHERE reference = $2 AND status = 'pending'`,
		eventID, reference)
	if err != nil {
		return false, fmt.Errorf("mark payin topup intent settled: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mark payin topup intent settled: rows affected: %w", err)
	}
	return n > 0, nil
}

func (r *repo) MarkTopupIntentExpired(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE payin_topup_intents SET status = 'expired', updated_at = now()
		WHERE id = $1 AND status = 'pending'`, id)
	if err != nil {
		return fmt.Errorf("mark payin topup intent expired: %w", err)
	}
	return nil
}
