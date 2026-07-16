//go:build integration

// Proves internal/ledger/feepolicy's CreateQuote/ConsumeQuote (docs/plan/38
// Task T2) against a real Postgres — the atomic UPDATE...WHERE that makes
// consumption single-use and mismatch-safe is exactly the kind of behavior
// sqlmock can't meaningfully verify under real concurrency.
package feepolicy_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/ledger/feepolicy"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/database"
)

func quoteMigrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations")
}

func setupQuoteTestDB(t *testing.T) *database.DBSQL {
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
	require.NoError(t, testutil.ApplyServiceMigrations(quoteMigrationsSourceURL(t), dsn))

	cfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 20,
	}
	db, err := database.New(ctx, cfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestQuote_CreateThenConsume_HappyPath(t *testing.T) {
	db := setupQuoteTestDB(t)
	policy := feepolicy.New(db)
	ctx := context.Background()
	userID := uuid.New()
	amount := decimal.NewFromInt(100_000)

	q, err := policy.CreateQuote(ctx, userID, "transfer_p2p", "", "IDR", amount, time.Minute)
	require.NoError(t, err)

	fee, feeGateway, err := policy.ConsumeQuote(ctx, db, q.ID, userID, "transfer_p2p", "IDR", amount, "tx:abc")
	require.NoError(t, err)
	assert.True(t, fee.Equal(q.FeeAmount))
	assert.Equal(t, q.FeeGateway, feeGateway)
}

func TestQuote_Expired_ErrQuoteExpired(t *testing.T) {
	db := setupQuoteTestDB(t)
	policy := feepolicy.New(db)
	ctx := context.Background()
	userID := uuid.New()
	amount := decimal.NewFromInt(50_000)

	q, err := policy.CreateQuote(ctx, userID, "money_in", "", "IDR", amount, time.Minute)
	require.NoError(t, err)
	// Force expiry directly — CreateQuote's ttl<=0 means "use the
	// configured default", not "create already expired", so backdating
	// expires_at is the only way to exercise real expiry deterministically.
	_, err = db.ExecContext(ctx, `UPDATE fee_quotes SET expires_at = now() - interval '1 minute' WHERE id = $1`, q.ID)
	require.NoError(t, err)

	_, _, err = policy.ConsumeQuote(ctx, db, q.ID, userID, "money_in", "IDR", amount, "tx:abc")
	assert.ErrorIs(t, err, feepolicy.ErrQuoteExpired)
}

func TestQuote_SecondConsume_ErrQuoteExpired(t *testing.T) {
	db := setupQuoteTestDB(t)
	policy := feepolicy.New(db)
	ctx := context.Background()
	userID := uuid.New()
	amount := decimal.NewFromInt(75_000)

	q, err := policy.CreateQuote(ctx, userID, "withdraw_settle", "", "IDR", amount, time.Minute)
	require.NoError(t, err)

	_, _, err = policy.ConsumeQuote(ctx, db, q.ID, userID, "withdraw_settle", "IDR", amount, "tx:first")
	require.NoError(t, err)

	_, _, err = policy.ConsumeQuote(ctx, db, q.ID, userID, "withdraw_settle", "IDR", amount, "tx:second")
	assert.ErrorIs(t, err, feepolicy.ErrQuoteExpired, "a second consume of an already-consumed quote must be indistinguishable from expired")
}

func TestQuote_AmountMismatch_ErrQuoteMismatch_QuoteStaysUnconsumed(t *testing.T) {
	db := setupQuoteTestDB(t)
	policy := feepolicy.New(db)
	ctx := context.Background()
	userID := uuid.New()
	amount := decimal.NewFromInt(100_000)
	wrongAmount := amount.Add(decimal.NewFromInt(1))

	q, err := policy.CreateQuote(ctx, userID, "transfer_p2p", "", "IDR", amount, time.Minute)
	require.NoError(t, err)

	_, _, err = policy.ConsumeQuote(ctx, db, q.ID, userID, "transfer_p2p", "IDR", wrongAmount, "tx:abc")
	assert.ErrorIs(t, err, feepolicy.ErrQuoteMismatch)

	var consumedAt sql.NullTime
	require.NoError(t, db.QueryRowContext(ctx, `SELECT consumed_at FROM fee_quotes WHERE id = $1`, q.ID).Scan(&consumedAt))
	assert.False(t, consumedAt.Valid, "a mismatched attempt must NOT burn the quote")

	// The SAME quote, with the CORRECT amount, must still be consumable.
	fee, _, err := policy.ConsumeQuote(ctx, db, q.ID, userID, "transfer_p2p", "IDR", amount, "tx:correct")
	require.NoError(t, err, "the quote must remain valid after a rejected mismatched attempt")
	assert.True(t, fee.Equal(q.FeeAmount))
}

func TestQuote_ConcurrentConsume_ExactlyOneWins(t *testing.T) {
	db := setupQuoteTestDB(t)
	policy := feepolicy.New(db)
	ctx := context.Background()
	userID := uuid.New()
	amount := decimal.NewFromInt(60_000)

	q, err := policy.CreateQuote(ctx, userID, "transfer_p2p", "", "IDR", amount, time.Minute)
	require.NoError(t, err)

	const concurrency = 10
	var wins, losses int64
	done := make(chan struct{}, concurrency)
	for i := 0; i < concurrency; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			_, _, cerr := policy.ConsumeQuote(ctx, db, q.ID, userID, "transfer_p2p", "IDR", amount, fmt.Sprintf("tx:racer-%d", n))
			if cerr == nil {
				atomic.AddInt64(&wins, 1)
			} else {
				atomic.AddInt64(&losses, 1)
			}
		}(i)
	}
	for i := 0; i < concurrency; i++ {
		<-done
	}
	assert.Equal(t, int64(1), wins, "exactly one concurrent consume must win")
	assert.Equal(t, int64(concurrency-1), losses)
}
