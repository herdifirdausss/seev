package repository

//go:generate mockgen -source=outbox_event_repository.go -destination=outbox_event_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

type OutboxRepository interface {
	InsertEvents(
		ctx context.Context,
		tx *sql.Tx,
		events []model.OutboxEvent,
	) error

	// ClaimPending atomically claims up to limit 'pending' events (oldest
	// first), marking them 'processing' in the same statement via SKIP
	// LOCKED — safe to call concurrently from multiple worker replicas.
	ClaimPending(ctx context.Context, limit int) ([]model.OutboxEventRecord, error)

	// ClaimFailedForRetry is like ClaimPending but for 'failed' events that
	// still have retries left, oldest-attempted first.
	ClaimFailedForRetry(ctx context.Context, limit int) ([]model.OutboxEventRecord, error)

	// MarkPublished marks an event as successfully delivered to the broker.
	MarkPublished(ctx context.Context, id uuid.UUID) error

	// MarkFailed marks a publish attempt as failed, increments retry_count,
	// and schedules next_attempt_at using exponential backoff with jitter
	// (docs/roadmap/archive/12 Task T2: base 30s, factor 2, cap 15m — see the SQL in
	// the implementation for the exact formula). The DB trigger
	// auto-converts to 'dead' once retry_count reaches max_retries.
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error

	// ReapStuck resets events that have been 'processing' for longer than
	// olderThan back to 'failed' (worker crashed between claim and mark).
	// Returns the number of events reaped.
	ReapStuck(ctx context.Context, olderThan time.Duration) (int, error)

	// CountPending returns the number of pending events and the created_at
	// of the oldest one (zero time if none pending), for lag monitoring.
	CountPending(ctx context.Context) (count int, oldestCreatedAt time.Time, err error)

	// CountByStatuses returns the number of events in each requested status,
	// in a single query — used for periodic gauge refresh (e.g. "pending",
	// "dead" together) so the two-gauge refresh in outbox_relay.go's
	// gaugeLoop is one round trip instead of two (docs/roadmap/archive/11 Task T6). A
	// status with zero matching rows is simply absent from the returned
	// map — callers must treat a missing key as 0, not as an error.
	CountByStatuses(ctx context.Context, statuses []string) (map[string]int, error)

	// ReplayDead resets one 'dead' event back to 'failed' with a clean
	// retry budget (retry_count=0, next_attempt_at=now()) so the relay's
	// normal ClaimFailedForRetry picks it up on the next tick
	// (docs/roadmap/archive/12 Task T3). Returns apperror.ErrTransactionNotFound-style
	// "not found" behavior via a zero rows-affected check — see
	// implementation — if id doesn't exist or isn't currently 'dead'.
	ReplayDead(ctx context.Context, id uuid.UUID) error

	// ReplayAllDead replays every 'dead' event older than olderThan (i.e.
	// created before that time), capped at 100 per call to prevent a replay
	// storm from suddenly flooding the broker. Returns the number replayed.
	ReplayAllDead(ctx context.Context, olderThan time.Time) (int, error)

	// ListDead returns 'dead' events oldest first (docs/roadmap/archive/25 Task T5) —
	// oldest first, not newest, because the oldest dead events are the ones
	// that have been silently unpublished the longest and are the most
	// urgent for an operator to triage/replay.
	ListDead(ctx context.Context, limit, offset int) ([]model.DeadOutboxEvent, error)
}

type outboxRepo struct {
	db database.DatabaseSQL
}

// NewOutboxRepository requires a DB handle for the relay worker's
// claim/mark/reap operations; InsertEvents always runs against the tx passed in.
func NewOutboxRepository(db database.DatabaseSQL) OutboxRepository {
	return &outboxRepo{db: db}
}

// scanClaimed reads the RETURNING rows shared by ClaimPending/ClaimFailedForRetry.
func scanClaimed(rows *sql.Rows) ([]model.OutboxEventRecord, error) {
	defer rows.Close()
	events := make([]model.OutboxEventRecord, 0)
	for rows.Next() {
		var e model.OutboxEventRecord
		var rawPayload []byte
		if err := rows.Scan(&e.ID, &e.AggregateType, &e.AggregateID, &e.EventType, &rawPayload, &e.RetryCount); err != nil {
			return nil, fmt.Errorf("scan claimed outbox event: %w", err)
		}
		if err := json.Unmarshal(rawPayload, &e.Payload); err != nil {
			return nil, fmt.Errorf("unmarshal outbox payload %s: %w", e.ID, err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed outbox events: %w", err)
	}
	return events, nil
}

const maxOutboxBatch = 50 // guard against unbounded INSERT

func (r *outboxRepo) InsertEvents(
	ctx context.Context,
	tx *sql.Tx,
	events []model.OutboxEvent,
) error {
	if len(events) == 0 {
		return nil
	}
	if len(events) > maxOutboxBatch {
		return fmt.Errorf("outbox batch too large: %d > %d", len(events), maxOutboxBatch)
	}

	args := make([]any, 0, len(events)*5)
	parts := make([]string, 0, len(events))
	for i, e := range events {
		payload, err := json.Marshal(e.Payload)
		if err != nil {
			return fmt.Errorf("marshal outbox event %q: %w", e.EventType, err)
		}
		b := i*5 + 1
		parts = append(parts, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,now())", b, b+1, b+2, b+3, b+4))
		// Time-ordered v7, not v4 (docs/roadmap/archive/11 Task T4) — keeps
		// outbox_events' primary-key btree insert-clustered, and as a
		// side benefit gives ClaimPending a natural id-based tiebreaker
		// consistent with created_at ordering.
		args = append(args, generalutil.NewV7(), e.AggregateType, e.AggregateID, e.EventType, payload)
	}

	q := "INSERT INTO outbox_events(id,aggregate_type,aggregate_id,event_type,payload,created_at) VALUES " +
		strings.Join(parts, ",")
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("batch insert outbox: %w", err)
	}
	return nil
}

func (r *outboxRepo) ClaimPending(ctx context.Context, limit int) ([]model.OutboxEventRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		WITH claimed AS (
			UPDATE outbox_events
			SET status = 'processing', last_attempted_at = now()
			WHERE id IN (
				SELECT id FROM outbox_events
				WHERE status = 'pending'
				ORDER BY created_at ASC
				LIMIT $1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING id, aggregate_type, aggregate_id, event_type, payload, retry_count
		)
		SELECT id, aggregate_type, aggregate_id, event_type, payload, retry_count FROM claimed`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("claim pending outbox events: %w", err)
	}
	return scanClaimed(rows)
}

func (r *outboxRepo) ClaimFailedForRetry(ctx context.Context, limit int) ([]model.OutboxEventRecord, error) {
	// [docs/roadmap/archive/12 Task T2] next_attempt_at gates eligibility now, not
	// last_attempted_at — an event with a future next_attempt_at (backoff
	// still pending) is skipped even though RetryInterval's ticker fires
	// every 30s regardless; the query is what enforces the actual wait.
	rows, err := r.db.QueryContext(ctx, `
		WITH claimed AS (
			UPDATE outbox_events
			SET status = 'processing', last_attempted_at = now()
			WHERE id IN (
				SELECT id FROM outbox_events
				WHERE status = 'failed' AND retry_count < max_retries
				  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
				ORDER BY next_attempt_at ASC NULLS FIRST
				LIMIT $1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING id, aggregate_type, aggregate_id, event_type, payload, retry_count
		)
		SELECT id, aggregate_type, aggregate_id, event_type, payload, retry_count FROM claimed`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("claim failed outbox events for retry: %w", err)
	}
	return scanClaimed(rows)
}

func (r *outboxRepo) MarkPublished(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE outbox_events SET status = 'published', published_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark outbox event published: %w", err)
	}
	return nil
}

func (r *outboxRepo) MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	if len(errMsg) > 1000 {
		errMsg = errMsg[:1000]
	}
	// status stays 'failed' unless the trigger promotes it to 'dead' once
	// retry_count reaches max_retries (trg_outbox_dead_letter).
	//
	// [docs/roadmap/archive/12 Task T2] next_attempt_at = now() + exponential backoff
	// with jitter, computed from the NEW retry_count (retry_count+1, since
	// SET expressions see the pre-update row — "retry_count" here still
	// reads the old value): base 30s, factor 2, cap 15m (900s), plus up to
	// 50% jitter on top so many events failing at once (e.g. a broker
	// outage) don't all retry in lockstep and hammer it the instant it
	// recovers. LEAST caps the exponential term before adding jitter, per
	// the formula in docs/roadmap/archive/12: delay = min(cap, base*2^retryCount) +
	// jitter(0, delay/2).
	_, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'failed', retry_count = retry_count + 1,
		    last_error = $1, last_attempted_at = now(),
		    next_attempt_at = now() + (
		        LEAST(900, 30 * POWER(2, retry_count + 1)) * (1 + random() * 0.5)
		    ) * INTERVAL '1 second'
		WHERE id = $2`,
		errMsg, id,
	)
	if err != nil {
		return fmt.Errorf("mark outbox event failed: %w", err)
	}
	return nil
}

func (r *outboxRepo) ReapStuck(ctx context.Context, olderThan time.Duration) (int, error) {
	// [docs/roadmap/archive/12 Task T2] retry_count is deliberately NOT incremented
	// here. ReapStuck only detects "a worker crashed/died between claiming
	// this event and marking a result" — it says nothing about whether the
	// publish itself was ever actually attempted against the broker. Only
	// MarkFailed (called after a real publish attempt fails) increments
	// retry_count. Incrementing it here would let a long broker outage
	// alone (repeatedly stuck → reaped → stuck → reaped) march an event to
	// 'dead' without it ever having been genuinely retried max_retries
	// times — exactly the bug this task fixes.
	// next_attempt_at = now() (not backed off) — a reaped event gets
	// another immediate shot, since the interruption was ours, not the
	// downstream broker's rejection.
	res, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'failed', next_attempt_at = now(),
		    last_error = 'reaped: stuck in processing past deadline'
		WHERE status = 'processing' AND last_attempted_at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(olderThan.Seconds())),
	)
	if err != nil {
		return 0, fmt.Errorf("reap stuck outbox events: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("reap stuck: rows affected: %w", err)
	}
	return int(affected), nil
}

func (r *outboxRepo) CountPending(ctx context.Context) (count int, oldestCreatedAt time.Time, err error) {
	var oldest sql.NullTime
	err = r.db.QueryRowContext(ctx, `
		SELECT count(*), min(created_at) FROM outbox_events WHERE status = 'pending'`,
	).Scan(&count, &oldest)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("count pending outbox events: %w", err)
	}
	if oldest.Valid {
		oldestCreatedAt = oldest.Time
	}
	return count, oldestCreatedAt, nil
}

func (r *outboxRepo) CountByStatuses(ctx context.Context, statuses []string) (map[string]int, error) {
	result := make(map[string]int, len(statuses))
	if len(statuses) == 0 {
		return result, nil
	}

	ph := make([]string, len(statuses))
	args := make([]any, len(statuses))
	for i, s := range statuses {
		ph[i] = fmt.Sprintf("$%d", i+1)
		args[i] = s
	}

	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT status, count(*)
		FROM   outbox_events
		WHERE  status IN (%s)
		GROUP  BY status`, strings.Join(ph, ",")),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("count outbox events by statuses: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan outbox status count: %w", err)
		}
		result[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox status counts: %w", err)
	}
	return result, nil
}

// maxReplayAllBatch caps ReplayAllDead per call — an admin operation should
// never be able to flood the broker with an unbounded burst of replays in
// one request (docs/roadmap/archive/12 Task T3). Call again to replay more.
const maxReplayAllBatch = 100

func (r *outboxRepo) ReplayDead(ctx context.Context, id uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'failed', retry_count = 0, next_attempt_at = now(),
		    last_error = COALESCE(last_error, '') || ' [replayed by admin]'
		WHERE id = $1 AND status = 'dead'`,
		id,
	)
	if err != nil {
		return fmt.Errorf("replay dead outbox event: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("replay dead: rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %s", apperror.ErrOutboxEventNotFound, id)
	}
	return nil
}

func (r *outboxRepo) ReplayAllDead(ctx context.Context, olderThan time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'failed', retry_count = 0, next_attempt_at = now(),
		    last_error = COALESCE(last_error, '') || ' [replayed by admin]'
		WHERE id IN (
			SELECT id FROM outbox_events
			WHERE status = 'dead' AND created_at < $1
			ORDER BY created_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)`,
		olderThan, maxReplayAllBatch,
	)
	if err != nil {
		return 0, fmt.Errorf("replay all dead outbox events: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("replay all dead: rows affected: %w", err)
	}
	return int(affected), nil
}

func (r *outboxRepo) ListDead(ctx context.Context, limit, offset int) ([]model.DeadOutboxEvent, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, event_type, retry_count, COALESCE(last_error, ''), created_at
		FROM outbox_events WHERE status = 'dead'
		ORDER BY created_at ASC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list dead outbox events: %w", err)
	}
	defer rows.Close()

	var out []model.DeadOutboxEvent
	for rows.Next() {
		var e model.DeadOutboxEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.RetryCount, &e.LastError, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan dead outbox event: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dead outbox events: %w", err)
	}
	return out, nil
}
