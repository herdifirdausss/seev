//go:build integration

// Proves the outbox backoff behavior from docs/plan/12 Task T2 against real
// Postgres — sqlmock can't execute POWER()/random() to verify the actual
// computed delay, and can't prove ReapStuck genuinely leaves retry_count
// untouched at the database level (only that the Go code sent a query
// without "retry_count +1" in the text).
package ledger_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/database"
)

// insertRawOutboxEvent inserts an outbox_events row directly via SQL,
// bypassing InsertEvents (which requires an open tx) — acceptable for test
// setup, same pattern as createUserCashAccount above.
func insertRawOutboxEvent(t *testing.T, db *database.DBSQL, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, status, created_at)
		VALUES ($1, 'ledger_transaction', $2, 'test.event', '{}'::jsonb, $3, now())`,
		id, uuid.New(), status)
	require.NoError(t, err)
	return id
}

type outboxEventRow struct {
	Status          string
	RetryCount      int
	NextAttemptAt   *time.Time
	LastAttemptedAt *time.Time
}

func getOutboxEvent(t *testing.T, db *database.DBSQL, id uuid.UUID) outboxEventRow {
	t.Helper()
	var row outboxEventRow
	err := db.QueryRowContext(context.Background(), `
		SELECT status, retry_count, next_attempt_at, last_attempted_at
		FROM outbox_events WHERE id = $1`, id,
	).Scan(&row.Status, &row.RetryCount, &row.NextAttemptAt, &row.LastAttemptedAt)
	require.NoError(t, err)
	return row
}

// TestSchemaContract_OutboxBackoff_MarkFailedSetsExponentialDelay proves
// next_attempt_at is set to base*2^retryCount (+ jitter, cap 15m) after each
// MarkFailed call, using base=30s per docs/plan/12 Task T2's formula.
func TestSchemaContract_OutboxBackoff_MarkFailedSetsExponentialDelay(t *testing.T) {
	db := setupSchemaTestDB(t)
	outboxRepo := repository.NewOutboxRepository(db)
	ctx := context.Background()

	id := insertRawOutboxEvent(t, db, "pending")

	// First failure: retry_count 0 -> 1, delay ~= 30s * 2^1 = 60s, + up to
	// 50% jitter -> window [60s, 90s].
	before := time.Now()
	require.NoError(t, outboxRepo.MarkFailed(ctx, id, "publish timeout"))
	row := getOutboxEvent(t, db, id)

	require.Equal(t, "failed", row.Status)
	require.Equal(t, 1, row.RetryCount)
	require.NotNil(t, row.NextAttemptAt)
	delay := row.NextAttemptAt.Sub(before)
	require.GreaterOrEqual(t, delay, 59*time.Second, "delay too short for retry_count=1")
	require.LessOrEqual(t, delay, 95*time.Second, "delay too long (cap/jitter formula wrong) for retry_count=1")

	// Second failure: retry_count 1 -> 2, delay ~= 30s * 2^2 = 120s, window
	// [120s, 180s].
	before2 := time.Now()
	require.NoError(t, outboxRepo.MarkFailed(ctx, id, "publish timeout again"))
	row2 := getOutboxEvent(t, db, id)

	require.Equal(t, 2, row2.RetryCount)
	require.NotNil(t, row2.NextAttemptAt)
	delay2 := row2.NextAttemptAt.Sub(before2)
	require.GreaterOrEqual(t, delay2, 119*time.Second, "delay too short for retry_count=2")
	require.LessOrEqual(t, delay2, 185*time.Second, "delay too long for retry_count=2")
	require.Greater(t, delay2, delay, "delay must grow with retry_count (exponential backoff)")
}

// TestSchemaContract_OutboxBackoff_MarkFailedDelay_RespectsCap proves the
// exponential delay is clamped at 15 minutes (900s) regardless of how high
// retry_count climbs — otherwise a long-broken broker would eventually push
// next_attempt_at days into the future.
func TestSchemaContract_OutboxBackoff_MarkFailedDelay_RespectsCap(t *testing.T) {
	db := setupSchemaTestDB(t)
	outboxRepo := repository.NewOutboxRepository(db)
	ctx := context.Background()

	id := insertRawOutboxEvent(t, db, "pending")

	// Drive retry_count up high enough that the uncapped exponential would
	// be enormous (30 * 2^10 = ~8.5 hours) — the cap must still hold at 15m.
	for i := 0; i < 10; i++ {
		require.NoError(t, outboxRepo.MarkFailed(ctx, id, "still failing"))
	}

	before := time.Now()
	require.NoError(t, outboxRepo.MarkFailed(ctx, id, "still failing"))
	row := getOutboxEvent(t, db, id)

	require.NotNil(t, row.NextAttemptAt)
	delay := row.NextAttemptAt.Sub(before)
	// Cap is 900s (15m) + up to 50% jitter = max 1350s. Give a small margin
	// for test execution time.
	require.LessOrEqual(t, delay, 1360*time.Second, "delay exceeded the 15m cap (+ jitter margin)")
}

// TestSchemaContract_OutboxBackoff_ReapStuck_DoesNotIncrementRetryCount is
// the core proof for docs/plan/12 Task T2: an event reaped multiple times
// (simulating a broker outage spanning several StuckAfter windows) must
// never have its retry_count touched by the reaper — only a genuine publish
// attempt (MarkFailed) may increment it. Otherwise repeated reaping alone
// could march an event to 'dead' without it ever having actually been tried
// against the broker max_retries times.
func TestSchemaContract_OutboxBackoff_ReapStuck_DoesNotIncrementRetryCount(t *testing.T) {
	db := setupSchemaTestDB(t)
	outboxRepo := repository.NewOutboxRepository(db)
	ctx := context.Background()

	id := insertRawOutboxEvent(t, db, "pending")

	// Simulate a worker having claimed it (status='processing') a long time
	// ago, then crashing before ever attempting the actual publish.
	_, err := db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'processing', last_attempted_at = now() - interval '20 minutes'
		WHERE id = $1`, id)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		n, err := outboxRepo.ReapStuck(ctx, 10*time.Minute)
		require.NoError(t, err)
		if i == 0 {
			require.Equal(t, 1, n, "first reap should catch the stuck event")
		}

		row := getOutboxEvent(t, db, id)
		require.Equal(t, 0, row.RetryCount, "reaper must never increment retry_count (iteration %d)", i)
		require.Equal(t, "failed", row.Status)

		// Re-stick it for the next iteration, same as a worker claiming
		// and crashing again.
		_, err = db.ExecContext(ctx, `
			UPDATE outbox_events
			SET status = 'processing', last_attempted_at = now() - interval '20 minutes'
			WHERE id = $1`, id)
		require.NoError(t, err)
	}
}

// TestSchemaContract_OutboxBackoff_ClaimFailedForRetry_RespectsNextAttemptAt
// proves ClaimFailedForRetry's query filter, not RetryInterval's ticker
// cadence, is what gates retry eligibility.
func TestSchemaContract_OutboxBackoff_ClaimFailedForRetry_RespectsNextAttemptAt(t *testing.T) {
	db := setupSchemaTestDB(t)
	outboxRepo := repository.NewOutboxRepository(db)
	ctx := context.Background()

	id := insertRawOutboxEvent(t, db, "failed")
	_, err := db.ExecContext(ctx, `
		UPDATE outbox_events SET next_attempt_at = now() + interval '1 hour' WHERE id = $1`, id)
	require.NoError(t, err)

	claimed, err := outboxRepo.ClaimFailedForRetry(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, claimed, "event with a future next_attempt_at must not be claimed")

	_, err = db.ExecContext(ctx, `
		UPDATE outbox_events SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, id)
	require.NoError(t, err)

	claimed, err = outboxRepo.ClaimFailedForRetry(ctx, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1, "event whose next_attempt_at has passed must be claimed")
	require.Equal(t, id, claimed[0].ID)
}

// TestSchemaContract_OutboxBackoff_ClaimFailedForRetry_NullNextAttemptAt_ClaimedImmediately
// proves a failed event that predates the backoff feature (next_attempt_at
// still NULL) is treated as immediately eligible, not permanently stuck.
func TestSchemaContract_OutboxBackoff_ClaimFailedForRetry_NullNextAttemptAt_ClaimedImmediately(t *testing.T) {
	db := setupSchemaTestDB(t)
	outboxRepo := repository.NewOutboxRepository(db)
	ctx := context.Background()

	id := insertRawOutboxEvent(t, db, "failed") // next_attempt_at stays NULL

	claimed, err := outboxRepo.ClaimFailedForRetry(ctx, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, id, claimed[0].ID)
}

// ─── Admin dead-letter replay (docs/plan/12 Task T3) ───────────────────────────

// TestSchemaContract_OutboxReplay_DeadEventBecomesFailedAndClaimable proves
// the full lifecycle: a dead-lettered event, replayed by an admin, goes back
// to 'failed' with a reset retry budget and immediately becomes eligible for
// the relay's normal ClaimFailedForRetry path — exactly as if it had just
// failed once for the first time.
func TestSchemaContract_OutboxReplay_DeadEventBecomesFailedAndClaimable(t *testing.T) {
	db := setupSchemaTestDB(t)
	outboxRepo := repository.NewOutboxRepository(db)
	ctx := context.Background()

	id := insertRawOutboxEvent(t, db, "dead")
	// chk_last_attempted requires last_attempted_at whenever retry_count != 0
	// — a dead event always has both set for real (it got there via
	// MarkFailed), so the test setup must match.
	_, err := db.ExecContext(ctx, `
		UPDATE outbox_events
		SET retry_count = 5, last_error = 'gave up', last_attempted_at = now()
		WHERE id = $1`, id)
	require.NoError(t, err)

	require.NoError(t, outboxRepo.ReplayDead(ctx, id))

	row := getOutboxEvent(t, db, id)
	require.Equal(t, "failed", row.Status)
	require.Equal(t, 0, row.RetryCount)
	require.NotNil(t, row.NextAttemptAt)
	require.WithinDuration(t, time.Now(), *row.NextAttemptAt, 5*time.Second)

	claimed, err := outboxRepo.ClaimFailedForRetry(ctx, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1, "replayed event must be immediately claimable")
	require.Equal(t, id, claimed[0].ID)
}

func TestSchemaContract_OutboxReplay_NonDeadEvent_Rejected(t *testing.T) {
	db := setupSchemaTestDB(t)
	outboxRepo := repository.NewOutboxRepository(db)
	ctx := context.Background()

	id := insertRawOutboxEvent(t, db, "failed") // not dead

	err := outboxRepo.ReplayDead(ctx, id)
	require.Error(t, err, "replaying a non-dead event must fail, not silently no-op")

	row := getOutboxEvent(t, db, id)
	require.Equal(t, "failed", row.Status, "status must be untouched")
}

func TestSchemaContract_OutboxReplay_NonExistentEvent_Rejected(t *testing.T) {
	db := setupSchemaTestDB(t)
	outboxRepo := repository.NewOutboxRepository(db)
	ctx := context.Background()

	err := outboxRepo.ReplayDead(ctx, uuid.New())
	require.Error(t, err)
}

// TestSchemaContract_OutboxReplay_ReplayAllDead_RespectsOlderThanAndCap
// proves ReplayAllDead only touches dead events older than the cutoff, and
// leaves newer ones (and non-dead ones) alone.
func TestSchemaContract_OutboxReplay_ReplayAllDead_RespectsOlderThanAndCap(t *testing.T) {
	db := setupSchemaTestDB(t)
	outboxRepo := repository.NewOutboxRepository(db)
	ctx := context.Background()

	oldDead := insertRawOutboxEvent(t, db, "dead")
	_, err := db.ExecContext(ctx, `UPDATE outbox_events SET created_at = now() - interval '1 day' WHERE id = $1`, oldDead)
	require.NoError(t, err)

	newDead := insertRawOutboxEvent(t, db, "dead") // created_at = now(), not older than cutoff
	stillFailed := insertRawOutboxEvent(t, db, "failed")

	cutoff := time.Now().Add(-time.Hour)
	n, err := outboxRepo.ReplayAllDead(ctx, cutoff)
	require.NoError(t, err)
	require.Equal(t, 1, n, "only the event older than cutoff should be replayed")

	require.Equal(t, "failed", getOutboxEvent(t, db, oldDead).Status)
	require.Equal(t, "dead", getOutboxEvent(t, db, newDead).Status, "event newer than cutoff must stay dead")
	require.Equal(t, "failed", getOutboxEvent(t, db, stillFailed).Status, "non-dead event must be untouched")
}

// TestSchemaContract_ListDead_ThenReplay_DisappearsFromList is docs/plan/25
// Task T5's required integration journey: a dead event shows up in
// ListDead, and after ReplayDead resets it to 'failed' it no longer does —
// proving the admin list endpoint and the replay endpoint agree on what
// "dead" means at the database level, not just independently against a
// mock.
func TestSchemaContract_ListDead_ThenReplay_DisappearsFromList(t *testing.T) {
	db := setupSchemaTestDB(t)
	outboxRepo := repository.NewOutboxRepository(db)
	ctx := context.Background()

	deadID := insertRawOutboxEvent(t, db, "dead")
	_, err := db.ExecContext(ctx, `
		UPDATE outbox_events SET last_error = 'broker unreachable', retry_count = 5, last_attempted_at = now()
		WHERE id = $1`, deadID)
	require.NoError(t, err)

	before, err := outboxRepo.ListDead(ctx, 50, 0)
	require.NoError(t, err)
	require.Len(t, before, 1)
	require.Equal(t, deadID, before[0].ID)
	require.Equal(t, "test.event", before[0].EventType)
	require.Equal(t, 5, before[0].RetryCount)
	require.Equal(t, "broker unreachable", before[0].LastError)

	require.NoError(t, outboxRepo.ReplayDead(ctx, deadID))

	after, err := outboxRepo.ListDead(ctx, 50, 0)
	require.NoError(t, err)
	require.Empty(t, after, "a replayed event must no longer appear in the dead list")
	require.Equal(t, "failed", getOutboxEvent(t, db, deadID).Status)
}
