package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/model"
)

type eventInboxRepository struct {
	db database.DatabaseSQL
}

func (r *eventInboxRepository) TryInsert(ctx context.Context, e model.InboxEvent) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO merchant_event_inbox (event_id, event_type, source, payload_hash, received_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (event_id) DO NOTHING`,
		e.EventID, e.EventType, e.Source, e.PayloadHash,
	)
	if err != nil {
		return false, fmt.Errorf("merchant: insert inbox event: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (r *eventInboxRepository) MarkProcessed(ctx context.Context, eventID uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_event_inbox SET processed_at = now(), processing_error = NULL WHERE event_id = $1`, eventID)
	if err != nil {
		return fmt.Errorf("merchant: mark inbox event processed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *eventInboxRepository) MarkFailed(ctx context.Context, eventID uuid.UUID, errMsg string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_event_inbox SET processing_error = $1 WHERE event_id = $2`, errMsg, eventID)
	if err != nil {
		return fmt.Errorf("merchant: mark inbox event failed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
