//go:build integration

// TestDurableSchedule_* are the C5 Part B evidence tests
// (docs/roadmap/active/61-c5-advanced-financial-products-period-close.md §3):
// PlanSchedule → occurrence materialises → ExecuteOccurrence posts the
// transfer and marks the occurrence succeeded → idempotent replay treats
// ErrAlreadyPosted as success → daily skip policy materialises skipped rows.
//
// All tests run against a throwaway Postgres container (testcontainers-go)
// with the full real migration set applied so schema + trigger correctness
// is tested alongside service correctness.
package ledger_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/schedule"
	"github.com/herdifirdausss/seev/services/ledger/internal/processors"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
)

// newDurableService wires DurableService against real repositories.
// It follows the same wiring pattern as newService/newInterestService and
// wires the txLookupAdapter so ExecuteOccurrence can link the ledger
// transaction ID (required for succeeded status).
func newDurableService(db *database.DBSQL) (*schedule.DurableService, repository.ScheduledTransactionRepository, repository.ScheduledOccurrenceRepository) {
	handleSvc, _ := newService(db)
	schedRepo := repository.NewScheduledTransactionRepository(db)
	occRepo := repository.NewScheduledOccurrenceRepository(db)
	txRepo := repository.NewTransactionRepository(db, schemaTestDigestRing())
	svc := schedule.NewDurable(schedRepo, occRepo, handleSvc, nil, slog.Default(), testSnapshotLoc())
	svc.SetTransactionLookup(&txLookupAdapter{repo: txRepo})
	return svc, schedRepo, occRepo
}

// createDurableSchedule inserts a scheduled_transactions row with the given
// kind and runDate via schedRepo.Create inside its own transaction.
func createDurableSchedule(t *testing.T, db *database.DBSQL, schedRepo repository.ScheduledTransactionRepository, userID, targetID uuid.UUID, kind string, runDate time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	scheduleID := uuid.New()
	payload, err := json.Marshal(model.ScheduleCommand{
		Type: "transfer_p2p", Version: 1, Amount: "500",
		TargetUserID: targetID,
	})
	require.NoError(t, err)
	require.NoError(t, db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return schedRepo.Create(ctx, tx, scheduleID, userID, payload, kind, runDate, nil, "integration-test")
	}))
	return scheduleID
}

// TestDurableSchedule_ExecuteOccurrence_HappyPath verifies the complete C5
// Part B path: PlanSchedule materialises one occurrence for a once-schedule,
// ExecuteOccurrence posts the transfer and marks the occurrence succeeded,
// and the execution-attempt row and schedule last_run_date are written.
func TestDurableSchedule_ExecuteOccurrence_HappyPath(t *testing.T) {
	db := setupSchemaTestDB(t)
	ctx := context.Background()

	durableSvc, schedRepo, occRepo := newDurableService(db)

	userA := uuid.New()
	userB := uuid.New()
	createUserCashAccount(t, db, userA)
	createUserCashAccount(t, db, userB)

	handleSvc, _ := newService(db)
	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "durable-happy-fund",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(50_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	runDate := dateOnly(time.Now())
	scheduleID := createDurableSchedule(t, db, schedRepo, userA, userB, "once", runDate)

	// PlanSchedule materialises one occurrence for the once-schedule.
	occurrences, err := durableSvc.PlanSchedule(ctx, scheduleID, runDate)
	require.NoError(t, err)
	require.Len(t, occurrences, 1, "once schedule must plan exactly one occurrence")

	occurrenceID := occurrences[0].ID

	// ExecuteOccurrence: posts the transfer, marks occurrence succeeded.
	ok, err := durableSvc.ExecuteOccurrence(ctx, occurrenceID, "test-worker")
	require.NoError(t, err)
	assert.True(t, ok, "ExecuteOccurrence must return true on success")

	// Occurrence must be succeeded with a linked ledger transaction.
	occ, err := occRepo.Get(ctx, occurrenceID)
	require.NoError(t, err)
	assert.Equal(t, model.ScheduleOccurrenceSucceeded, occ.Status, "occurrence must be succeeded")
	assert.NotNil(t, occ.LedgerTransactionID, "succeeded occurrence must carry a ledger transaction ID")

	// At least one execution-attempt row must have been written.
	attempts, err := occRepo.ListAttempts(ctx, occurrenceID)
	require.NoError(t, err)
	assert.NotEmpty(t, attempts, "at least one attempt row must exist after execution")

	// Exactly one ledger transaction must exist for the occurrence key.
	idemKey := schedule.OccurrenceIdempotencyKey(scheduleID, runDate)
	assert.Equal(t, 1, countLedgerTransactions(t, db, idemKey), "exactly one ledger transaction must exist")

	// Schedule last_run_date must be set.
	row, err := schedRepo.GetByID(ctx, scheduleID)
	require.NoError(t, err)
	assert.NotNil(t, row.LastRunDate, "schedule last_run_date must be set after execution")
}

// TestDurableSchedule_IdempotentReplay_ErrAlreadyPosted simulates the crash
// window: the transfer committed but the occurrence was never marked succeeded.
// ExecuteOccurrence must treat ErrAlreadyPosted as success, mark the
// occurrence succeeded, and NOT create a second ledger transaction.
func TestDurableSchedule_IdempotentReplay_ErrAlreadyPosted(t *testing.T) {
	db := setupSchemaTestDB(t)
	ctx := context.Background()

	durableSvc, schedRepo, occRepo := newDurableService(db)

	userA := uuid.New()
	userB := uuid.New()
	createUserCashAccount(t, db, userA)
	createUserCashAccount(t, db, userB)

	handleSvc, _ := newService(db)
	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "durable-idem-fund",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(50_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	runDate := dateOnly(time.Now())
	scheduleID := createDurableSchedule(t, db, schedRepo, userA, userB, "once", runDate)

	// Pre-seed the ledger transaction with the same idempotency key and scope
	// that ExecuteOccurrence would use, simulating a prior crashed run.
	idemKey := schedule.OccurrenceIdempotencyKey(scheduleID, runDate)
	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey:   idemKey,
		IdempotencyScope: "schedule:" + userA.String(),
		Type:             "transfer_p2p",
		Amount:           decimal.NewFromInt(500),
		UserID:           userA,
		TargetUserID:     userB,
	}))

	// PlanSchedule materialises the occurrence (occurrence is still 'planned').
	occurrences, err := durableSvc.PlanSchedule(ctx, scheduleID, runDate)
	require.NoError(t, err)
	require.Len(t, occurrences, 1)
	occurrenceID := occurrences[0].ID

	// ExecuteOccurrence: poster returns ErrAlreadyPosted → treated as success.
	ok, err := durableSvc.ExecuteOccurrence(ctx, occurrenceID, "test-worker")
	require.NoError(t, err)
	assert.True(t, ok, "ErrAlreadyPosted must be treated as a successful execution")

	occ, err := occRepo.Get(ctx, occurrenceID)
	require.NoError(t, err)
	assert.Equal(t, model.ScheduleOccurrenceSucceeded, occ.Status, "occurrence must be succeeded on idempotent replay")
	assert.NotNil(t, occ.LedgerTransactionID, "occurrence must be linked to the pre-seeded transaction")

	// Exactly one transaction must exist: no second post from the replay.
	assert.Equal(t, 1, countLedgerTransactions(t, db, idemKey), "idempotent replay must not create a second ledger transaction")
}

// TestDurableSchedule_PlanSchedule_DailySkipPolicy verifies that a daily
// schedule with 'skip' missed-run policy materialises three skipped_missed
// occurrences for overdue dates and one claimable occurrence for today.
func TestDurableSchedule_PlanSchedule_DailySkipPolicy(t *testing.T) {
	db := setupSchemaTestDB(t)
	ctx := context.Background()

	durableSvc, schedRepo, occRepo := newDurableService(db)

	userA := uuid.New()
	userB := uuid.New()
	createUserCashAccount(t, db, userA)
	createUserCashAccount(t, db, userB)

	// Daily schedule starting 3 days ago; Create SQL sets missed_run_policy='skip'
	// for 'daily' kind, so missed dates are materialised as skipped_missed.
	threeDaysAgo := dateOnly(time.Now().AddDate(0, 0, -3))
	scheduleID := createDurableSchedule(t, db, schedRepo, userA, userB, "daily", threeDaysAgo)

	today := dateOnly(time.Now())
	plannedOccurrences, err := durableSvc.PlanSchedule(ctx, scheduleID, today)
	require.NoError(t, err)
	require.Len(t, plannedOccurrences, 1, "skip policy must plan only today's occurrence")

	// Today's occurrence must be in a claimable state.
	todayOcc, err := occRepo.Get(ctx, plannedOccurrences[0].ID)
	require.NoError(t, err)
	assert.True(t,
		todayOcc.Status == model.ScheduleOccurrencePlanned ||
			todayOcc.Status == model.ScheduleOccurrenceDue ||
			todayOcc.Status == model.ScheduleOccurrenceReady,
		"today's occurrence must be claimable, got: %s", todayOcc.Status)

	// Four rows total: 3 skipped + 1 planned.
	allOccs, err := occRepo.List(ctx, scheduleID, userA, 10, 0)
	require.NoError(t, err)
	assert.Len(t, allOccs, 4, "4 occurrence rows total (3 skipped_missed + 1 planned)")

	skipped := 0
	for _, item := range allOccs {
		if item.Status == model.ScheduleOccurrenceSkippedMissed {
			skipped++
		}
	}
	assert.Equal(t, 3, skipped, "3 overdue occurrences must be skipped_missed")
}
