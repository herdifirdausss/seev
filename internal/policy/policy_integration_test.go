//go:build integration

// Proves internal/policy's SQL (repository.go) against a real Postgres —
// unit tests (policy_test.go) mock Repository entirely, so they can't catch
// a bad query, a wrong column, or a constraint violation. Runs its own
// throwaway container + migrations, independent of internal/ledger's own
// schema_contract_test.go (docs/plan/17 Task T1: policy has no dependency
// on the ledger module in either direction, including in tests).
package policy_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/policy"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
)

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}

func setupPolicyTestDB(t *testing.T) *database.DBSQL {
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

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPassword, host, port.Port(), dbName)
	require.NoError(t, testutil.ApplyServiceMigrations(migrationsSourceURL(t), dsn))

	db, err := database.New(ctx, config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	}.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func int64Ptr(v int64) *int64 { return &v }

// TestPolicy_RepositoryRoundTrip proves Upsert/GetEffective/List against
// real Postgres: user override beats type-wide default, disabled rows are
// still returned by List (not silently filtered), and the UNIQUE partial
// index on (transaction_type) WHERE user_id IS NULL actually holds.
func TestPolicy_RepositoryRoundTrip(t *testing.T) {
	db := setupPolicyTestDB(t)
	repo := policy.NewRepository(db)
	ctx := context.Background()

	userID := uuid.New()

	// No row at all yet.
	_, found, err := repo.GetEffective(ctx, userID, "transfer_p2p")
	require.NoError(t, err)
	require.False(t, found)

	// Type-wide default.
	_, err = repo.Upsert(ctx, policy.Limit{
		TransactionType: "transfer_p2p", MaxPerTx: int64Ptr(5000), Enabled: true,
	})
	require.NoError(t, err)

	l, found, err := repo.GetEffective(ctx, userID, "transfer_p2p")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(5000), *l.MaxPerTx)
	require.Nil(t, l.UserID)

	// User-specific override.
	_, err = repo.Upsert(ctx, policy.Limit{
		UserID: &userID, TransactionType: "transfer_p2p", MaxPerTx: int64Ptr(500), Enabled: true,
	})
	require.NoError(t, err)

	l, found, err = repo.GetEffective(ctx, userID, "transfer_p2p")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(500), *l.MaxPerTx, "user override must win over the type-wide default")
	require.NotNil(t, l.UserID)

	// A different user still gets the default.
	otherUser := uuid.New()
	l, found, err = repo.GetEffective(ctx, otherUser, "transfer_p2p")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(5000), *l.MaxPerTx)

	// List returns both rows.
	all, err := repo.List(ctx, "transfer_p2p", nil)
	require.NoError(t, err)
	require.Len(t, all, 2)
}

// TestPolicy_RepositoryRoundTrip_DuplicateDefaultRejected proves the
// partial UNIQUE index (transaction_type) WHERE user_id IS NULL — a second
// type-wide default for the same type must be rejected at the DB level via
// ON CONFLICT upsert semantics (not a duplicate row).
func TestPolicy_Upsert_SecondDefaultUpdatesNotDuplicates(t *testing.T) {
	db := setupPolicyTestDB(t)
	repo := policy.NewRepository(db)
	ctx := context.Background()

	_, err := repo.Upsert(ctx, policy.Limit{TransactionType: "money_in", MaxPerTx: int64Ptr(1000), Enabled: true})
	require.NoError(t, err)
	_, err = repo.Upsert(ctx, policy.Limit{TransactionType: "money_in", MaxPerTx: int64Ptr(2000), Enabled: true})
	require.NoError(t, err)

	all, err := repo.List(ctx, "money_in", nil)
	require.NoError(t, err)
	require.Len(t, all, 1, "second upsert of the same type-wide default must UPDATE, not create a duplicate row")
	require.Equal(t, int64(2000), *all[0].MaxPerTx)
}

// TestPolicy_Engine_CacheExpiresAndRefetchesFromRealDB is the specific test
// docs/plan/17 Task T1's own "Test wajib" calls for: upsert a limit via the
// repository, Check must read the NEW value once the in-process cache TTL
// has passed — using an injected small TTL (WithCacheTTL), never a 60s+
// sleep.
func TestPolicy_Engine_CacheExpiresAndRefetchesFromRealDB(t *testing.T) {
	db := setupPolicyTestDB(t)
	repo := policy.NewRepository(db)
	counter := cache.NewMemoryCounter()
	defer counter.Stop()
	engine := policy.New(repo, counter, time.UTC, nil, policy.WithCacheTTL(20*time.Millisecond))
	ctx := context.Background()
	userID := uuid.New()

	_, err := repo.Upsert(ctx, policy.Limit{TransactionType: "transfer_p2p", MaxPerTx: int64Ptr(1000), Enabled: true})
	require.NoError(t, err)

	allowed, _, _, err := engine.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(1000))
	require.NoError(t, err)
	require.True(t, allowed)

	allowed, _, _, err = engine.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(1001))
	require.NoError(t, err)
	require.False(t, allowed, "1001 must exceed the 1000 limit already in effect")

	// Tighten the limit in the DB — Check must still see the STALE cached
	// value immediately after (within the TTL window).
	_, err = repo.Upsert(ctx, policy.Limit{TransactionType: "transfer_p2p", MaxPerTx: int64Ptr(100), Enabled: true})
	require.NoError(t, err)

	allowed, _, _, err = engine.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(500))
	require.NoError(t, err)
	require.True(t, allowed, "within the cache TTL window, Check must still use the OLD limit (1000), not hit the DB every call")

	time.Sleep(30 * time.Millisecond)

	allowed, rule, _, err := engine.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(500))
	require.NoError(t, err)
	require.False(t, allowed, "after the TTL passes, Check must re-fetch and see the NEW tighter limit (100)")
	require.Equal(t, "max_per_tx", rule)
}

// TestPolicy_Engine_DailyVelocity_RealDBAndRealCounter is an end-to-end
// proof of the whole stack (repository + engine + counter) against a
// realistic scenario: two postings in one day against a daily amount limit.
func TestPolicy_Engine_DailyVelocity_RealDBAndRealCounter(t *testing.T) {
	db := setupPolicyTestDB(t)
	repo := policy.NewRepository(db)
	counter := cache.NewMemoryCounter()
	defer counter.Stop()
	engine := policy.New(repo, counter, time.UTC, nil)
	ctx := context.Background()
	userID := uuid.New()

	_, err := repo.Upsert(ctx, policy.Limit{
		TransactionType: "transfer_p2p", MaxDailyAmount: int64Ptr(10000), Enabled: true,
	})
	require.NoError(t, err)

	allowed, _, _, err := engine.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(6000))
	require.NoError(t, err)
	require.True(t, allowed)
	engine.Record(ctx, userID, "transfer_p2p", decimal.NewFromInt(6000))

	allowed, _, _, err = engine.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(4000))
	require.NoError(t, err)
	require.True(t, allowed, "6000 + 4000 == 10000, exactly at the limit")
	engine.Record(ctx, userID, "transfer_p2p", decimal.NewFromInt(4000))

	allowed, rule, _, err := engine.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(1))
	require.NoError(t, err)
	require.False(t, allowed, "10000 already used, limit is 10000 — even 1 more must be rejected")
	require.Equal(t, "max_daily_amount", rule)
}
