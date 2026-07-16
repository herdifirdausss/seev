package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ─── CountByStatuses (docs/plan/11 Task T6) ────────────────────────────────────

func TestCountByStatuses_SingleQuery_ReturnsAllRequestedCounts(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewOutboxRepository(db)
	ctx := context.Background()

	mock.ExpectQuery(`SELECT status, count\(\*\)`).
		WillReturnRows(sqlmock.NewRows([]string{"status", "count"}).
			AddRow("pending", 7).
			AddRow("dead", 2))

	counts, err := repo.CountByStatuses(ctx, []string{"pending", "dead"})

	require.NoError(t, err)
	require.Equal(t, map[string]int{"pending": 7, "dead": 2}, counts)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCountByStatuses_StatusWithNoRows_AbsentFromMap(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewOutboxRepository(db)
	ctx := context.Background()

	// GROUP BY simply omits a status with zero matching rows — the
	// repository doesn't backfill zeros, callers must treat a missing key
	// as 0 (see outbox_relay.go refreshGauges' counts["dead"] access).
	mock.ExpectQuery(`SELECT status, count\(\*\)`).
		WillReturnRows(sqlmock.NewRows([]string{"status", "count"}).
			AddRow("pending", 3))

	counts, err := repo.CountByStatuses(ctx, []string{"pending", "dead"})

	require.NoError(t, err)
	require.Equal(t, map[string]int{"pending": 3}, counts)
	_, hasDead := counts["dead"]
	require.False(t, hasDead)
}

func TestCountByStatuses_EmptyInput_NoQuery(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewOutboxRepository(db)
	ctx := context.Background()

	counts, err := repo.CountByStatuses(ctx, nil)

	require.NoError(t, err)
	require.Empty(t, counts)
	require.NoError(t, mock.ExpectationsWereMet(), "no SQL should be sent for empty statuses")
}

// ─── Outbox backoff (docs/plan/12 Task T2) ─────────────────────────────────────
//
// sqlmock only matches the query text against a regex and returns a canned
// result — it cannot execute Postgres's POWER()/random() to prove the
// backoff math is right, or that ReapStuck genuinely leaves retry_count
// untouched at the database level. These tests only confirm the Go-level
// plumbing (correct query shape, no panics, correct row-count reporting);
// the real proof is the integration test in schema_contract_test.go
// (TestSchemaContract_OutboxBackoff_*), run against real Postgres.

func TestMarkFailed_ComputesNextAttemptAt(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewOutboxRepository(db)
	ctx := context.Background()

	mock.ExpectExec(`UPDATE outbox_events\s+SET status = 'failed', retry_count = retry_count \+ 1,\s+last_error = \$1, last_attempted_at = now\(\),\s+next_attempt_at = now\(\) \+ \(`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.MarkFailed(ctx, uuid.New(), "publish timeout")

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReapStuck_DoesNotIncrementRetryCount(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewOutboxRepository(db)
	ctx := context.Background()

	// The regex explicitly excludes "retry_count" from the SET clause —
	// if a future edit reintroduces the increment, this query text would
	// no longer match and the test fails.
	mock.ExpectExec(`UPDATE outbox_events\s+SET status = 'failed', next_attempt_at = now\(\),\s+last_error = 'reaped: stuck in processing past deadline'\s+WHERE status = 'processing'`).
		WillReturnResult(sqlmock.NewResult(0, 2))

	n, err := repo.ReapStuck(ctx, 10*time.Minute)

	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.NoError(t, mock.ExpectationsWereMet())
}
