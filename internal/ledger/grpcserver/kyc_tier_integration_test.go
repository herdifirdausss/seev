//go:build integration

// Proves docs/roadmap/archive/39 Task T5: ApplyKycTier upserts policy_limits from the
// policy_tier_limits template, in-place on upgrade, idempotently, and the
// policy engine actually enforces the new cap — against a real Postgres,
// same throwaway-container pattern as server_integration_test.go's
// TestPostMoneyInEndToEndOverGRPC.
package grpcserver_test

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
	"github.com/herdifirdausss/seev/internal/ledger"
	"github.com/herdifirdausss/seev/internal/policy"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
)

// setupKycTierTestServer boots a fresh throwaway Postgres, wires a full
// ledger.Module behind a real gRPC server, and returns a client plus the raw
// db handle for assertions — factored out of
// TestPostMoneyInEndToEndOverGRPC's inline setup since this file adds
// several independent ApplyKycTier test functions that all need it.
func setupKycTierTestServer(t *testing.T) (ledgerv1.LedgerServiceClient, *database.DBSQL) {
	t.Helper()
	ctx := context.Background()
	const dbName, dbUser, dbPassword = "seev_ledger_test", "test", "secret"
	container, err := postgres.Run(ctx, "postgres:16.14-alpine",
		postgres.WithDatabase(dbName), postgres.WithUsername(dbUser), postgres.WithPassword(dbPassword),
		postgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5432")
	require.NoError(t, err)
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPassword, host, port.Port(), dbName)
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	migrations := "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations")
	require.NoError(t, testutil.ApplyServiceMigrations(migrations, dsn))

	db, err := database.New(ctx, database.Config{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	module := ledger.NewModule(db, nil, nil, ledger.WorkerConfig{}, slog.Default(), decimal.Zero, nil, nil, 0)

	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	module.RegisterGRPC(grpcServer)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)
	connectCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := grpc.DialContext(connectCtx, "bufnet", //nolint:staticcheck // bufconn requires the legacy blocking dial API.
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(), //nolint:staticcheck
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return ledgerv1.NewLedgerServiceClient(conn), db
}

func policyLimitsRowCount(t *testing.T, db *database.DBSQL, userID uuid.UUID) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM policy_limits WHERE user_id = $1`, userID).Scan(&count))
	return count
}

func policyLimitMaxPerTx(t *testing.T, db *database.DBSQL, userID uuid.UUID, txType string) int64 {
	t.Helper()
	var maxPerTx int64
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT max_per_tx FROM policy_limits WHERE user_id = $1 AND transaction_type = $2`, userID, txType).Scan(&maxPerTx))
	return maxPerTx
}

func TestApplyKycTier_L1ThenL2_UpgradesInPlace(t *testing.T) {
	client, db := setupKycTierTestServer(t)
	ctx := context.Background()
	userID := uuid.New()

	_, err := client.ApplyKycTier(ctx, &ledgerv1.ApplyKycTierRequest{UserId: userID.String(), KycLevel: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(1_000_000), policyLimitMaxPerTx(t, db, userID, "transfer_p2p"))
	assert.Equal(t, 3, policyLimitsRowCount(t, db, userID), "one row per policy_tier_limits transaction_type at L1")

	_, err = client.ApplyKycTier(ctx, &ledgerv1.ApplyKycTierRequest{UserId: userID.String(), KycLevel: 2})
	require.NoError(t, err)
	assert.Equal(t, int64(100_000_000), policyLimitMaxPerTx(t, db, userID, "transfer_p2p"),
		"upgrading to L2 must overwrite the L1 row's values in place")
	assert.Equal(t, 3, policyLimitsRowCount(t, db, userID),
		"still exactly 3 rows — upgrade updates in place, never inserts a second row per type")
}

func TestApplyKycTier_PolicyEngineEnforcesNewCap(t *testing.T) {
	client, db := setupKycTierTestServer(t)
	ctx := context.Background()
	userID := uuid.New()

	_, err := client.ApplyKycTier(ctx, &ledgerv1.ApplyKycTierRequest{UserId: userID.String(), KycLevel: 1})
	require.NoError(t, err)

	// A fresh Engine per phase sidesteps the Engine's own in-process limit
	// cache (default 60s TTL) — this test is about ApplyKycTier's effect on
	// the DB, not the cache's staleness window.
	engineL1 := policy.New(policy.NewRepository(db), cache.NewMemoryCounter(), time.UTC, slog.Default())
	allowed, rule, _, err := engineL1.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(2_000_000))
	require.NoError(t, err)
	assert.False(t, allowed, "L1's max_per_tx is 1,000,000 — a 2,000,000 transfer must be rejected")
	assert.Equal(t, "max_per_tx", rule)

	allowed, _, _, err = engineL1.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(500_000))
	require.NoError(t, err)
	assert.True(t, allowed, "500,000 is under the L1 cap")

	_, err = client.ApplyKycTier(ctx, &ledgerv1.ApplyKycTierRequest{UserId: userID.String(), KycLevel: 2})
	require.NoError(t, err)

	engineL2 := policy.New(policy.NewRepository(db), cache.NewMemoryCounter(), time.UTC, slog.Default())
	allowed, _, _, err = engineL2.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(2_000_000))
	require.NoError(t, err)
	assert.True(t, allowed, "after upgrading to L2 (max_per_tx 100,000,000), the same 2,000,000 transfer must now pass")
}

func TestApplyKycTier_UnknownLevel_InvalidArgument(t *testing.T) {
	client, _ := setupKycTierTestServer(t)
	ctx := context.Background()

	_, err := client.ApplyKycTier(ctx, &ledgerv1.ApplyKycTierRequest{UserId: uuid.New().String(), KycLevel: 3})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestApplyKycTier_Idempotent_ApplyingTwiceIsOneResult(t *testing.T) {
	client, db := setupKycTierTestServer(t)
	ctx := context.Background()
	userID := uuid.New()

	_, err := client.ApplyKycTier(ctx, &ledgerv1.ApplyKycTierRequest{UserId: userID.String(), KycLevel: 1})
	require.NoError(t, err)
	_, err = client.ApplyKycTier(ctx, &ledgerv1.ApplyKycTierRequest{UserId: userID.String(), KycLevel: 1})
	require.NoError(t, err)

	assert.Equal(t, 3, policyLimitsRowCount(t, db, userID), "re-applying the same level must not create duplicate rows")
	assert.Equal(t, int64(1_000_000), policyLimitMaxPerTx(t, db, userID, "transfer_p2p"))
}

func TestApplyKycTier_L0HardControlBlocksPositiveAmount(t *testing.T) {
	client, db := setupKycTierTestServer(t)
	ctx := context.Background()
	userID := uuid.New()

	_, err := client.ApplyKycTier(ctx, &ledgerv1.ApplyKycTierRequest{UserId: userID.String(), KycLevel: 0})
	require.NoError(t, err)
	assert.Equal(t, int64(0), policyLimitMaxPerTx(t, db, userID, "transfer_p2p"))
	assert.Equal(t, 3, policyLimitsRowCount(t, db, userID))

	engine := policy.New(policy.NewRepository(db), cache.NewMemoryCounter(), time.UTC, slog.Default())
	allowed, rule, _, err := engine.Check(ctx, userID, "transfer_p2p", decimal.NewFromInt(1))
	require.NoError(t, err)
	assert.False(t, allowed, "L0's zero max_per_tx must reject any positive amount")
	assert.Equal(t, "max_per_tx", rule)
}
