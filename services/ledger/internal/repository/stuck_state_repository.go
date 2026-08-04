package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

type StuckStateRepository interface {
	ReadStuckState(context.Context, time.Time) (model.StuckStateSnapshot, error)
}

type stuckStateRepo struct{ db database.DatabaseSQL }

func NewStuckStateRepository(db database.DatabaseSQL) StuckStateRepository {
	return &stuckStateRepo{db: db}
}

func (r *stuckStateRepo) ReadStuckState(ctx context.Context, now time.Time) (model.StuckStateSnapshot, error) {
	var snapshot model.StuckStateSnapshot
	var oldest sql.NullTime
	if err := r.db.QueryRowContext(ctx, `SELECT count(*), min(created_at) FROM outbox_events WHERE status IN ('pending','failed','processing')`).Scan(&snapshot.OutboxPendingCount, &oldest); err != nil {
		return model.StuckStateSnapshot{}, err
	}
	if oldest.Valid {
		snapshot.OutboxOldestPendingAt = oldest.Time
	}
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_occurrences WHERE status IN ('due','ready','retry_wait') AND (next_attempt_at IS NULL OR next_attempt_at <= $1)`, now).Scan(&snapshot.ScheduleDueCount); err != nil {
		return model.StuckStateSnapshot{}, err
	}
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_occurrences WHERE status='processing' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $1`, now).Scan(&snapshot.ScheduleProcessingCount); err != nil {
		return model.StuckStateSnapshot{}, err
	}
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM chargeback_disputes WHERE status IN ('open','evidence_submitted') AND evidence_due_at IS NOT NULL AND evidence_due_at <= $1`, now).Scan(&snapshot.DisputeDueCount); err != nil {
		return model.StuckStateSnapshot{}, err
	}
	return snapshot, nil
}
