//go:build integration

package repository_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/payout/repository"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/database"
)

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations")
}

func setupTestDB(t *testing.T) *database.DBSQL {
	t.Helper()
	ctx := context.Background()

	const dbName, dbUser, dbPassword = "seev_test", "test", "secret"

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5432")
	require.NoError(t, err)

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		dbUser, dbPassword, host, port.Port(), dbName)

	require.NoError(t, testutil.ApplyServiceMigrations(migrationsSourceURL(t), dsn))

	cfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	}
	db, err := database.New(ctx, cfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

func newTestRequest() model.PayoutRequest {
	return model.PayoutRequest{
		ID: uuid.New(), UserID: uuid.New(), Amount: decimal.NewFromInt(100_000), Currency: "IDR",
		Vendor: "mockvendor", Destination: []byte(`{"bank_code":"014","account_no":"1234567890"}`),
		CreatedBy: "test",
	}
}

// TestTransitionToHeld_ConcurrentCallers_ExactlyOneWins is docs/plan/23
// Task T1's required race test: the atomic conditional UPDATE (WHERE
// status = 'created') is the sole concurrency guard for this transition —
// two goroutines racing to hold the SAME request must result in exactly
// one winner, never both, never neither.
func TestTransitionToHeld_ConcurrentCallers_ExactlyOneWins(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req))

	const concurrency = 10
	var wonCount int64
	var wg sync.WaitGroup
	holdTxIDs := make([]uuid.UUID, concurrency)
	for i := 0; i < concurrency; i++ {
		holdTxIDs[i] = uuid.New()
	}

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			won, err := repo.TransitionToHeld(ctx, req.ID, holdTxIDs[idx])
			assert.NoError(t, err)
			if won {
				atomic.AddInt64(&wonCount, 1)
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int64(1), wonCount, "exactly one of %d concurrent transitions must win", concurrency)

	final, err := repo.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusHeld, final.Status)
	require.NotNil(t, final.HoldTxID)

	found := false
	for _, id := range holdTxIDs {
		if id == *final.HoldTxID {
			found = true
			break
		}
	}
	assert.True(t, found, "final hold_tx_id must be one of the attempted values, not corrupted")
}

// TestTransitionToHeld_WrongStartingStatus_NoOp proves the guard also
// rejects a transition attempted from the wrong status (not just
// concurrent same-status races).
func TestTransitionToHeld_WrongStartingStatus_NoOp(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req))

	firstTxID := uuid.New()
	won, err := repo.TransitionToHeld(ctx, req.ID, firstTxID)
	require.NoError(t, err)
	require.True(t, won)

	// Second attempt — request is already 'held', not 'created'.
	secondTxID := uuid.New()
	won2, err := repo.TransitionToHeld(ctx, req.ID, secondTxID)
	require.NoError(t, err)
	assert.False(t, won2, "a transition from the wrong starting status must be a no-op, not an error")

	final, err := repo.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, firstTxID, *final.HoldTxID, "hold_tx_id must remain the FIRST winner's value")
}

func TestFullLifecycle_CreatedToSettled(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req))

	holdTxID := uuid.New()
	won, err := repo.TransitionToHeld(ctx, req.ID, holdTxID)
	require.NoError(t, err)
	require.True(t, won)

	won, err = repo.TransitionToSubmitted(ctx, req.ID)
	require.NoError(t, err)
	require.True(t, won)

	won, err = repo.TransitionToVendorPending(ctx, req.ID, "vendor-ref-1")
	require.NoError(t, err)
	require.True(t, won)

	settleTxID := uuid.New()
	won, err = repo.TransitionToSettled(ctx, req.ID, settleTxID)
	require.NoError(t, err)
	require.True(t, won)

	final, err := repo.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSettled, final.Status)
	assert.Equal(t, "vendor-ref-1", final.VendorRef)
	require.NotNil(t, final.SettleTxID)
	assert.Equal(t, settleTxID, *final.SettleTxID)
}

func TestInsertVendorCall_And_ListStuck(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req))

	require.NoError(t, repo.InsertVendorCall(ctx, model.PayoutVendorCall{
		ID: uuid.New(), PayoutRequestID: req.ID, Attempt: 1, ReqSummary: "submit amount=100000 vendor=mockvendor",
		RespStatus: "pending", Outcome: model.VendorCallAccepted,
	}))

	// The row's updated_at is "now" — a cutoff set to the future means
	// "updated_at < cutoff" is true, so the row must show up as stuck.
	stuck, err := repo.ListStuck(ctx, model.StatusCreated, time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	assert.NotEmpty(t, stuck, "a future cutoff must surface the just-created request")

	// A cutoff in the past must NOT surface a freshly-updated row.
	notStuck, err := repo.ListStuck(ctx, model.StatusCreated, time.Now().Add(-time.Hour), 10)
	require.NoError(t, err)
	for _, r := range notStuck {
		assert.NotEqual(t, req.ID, r.ID, "a past cutoff must not surface a request updated after it")
	}
}
