//go:build integration

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// applyLedgerMigrations runs the real services/ledger/migrations/*.up.sql files
// against dsn — inlined rather than internal/testkit.ApplyMigration
// (a boundary_test.go rule: tools/loaddataset, a standalone read-only tool
// with no owned internal/ module, may not import internal/testkit; the
// golang-migrate library it thinly wraps is not an internal/ package, so
// using it directly here stays inside the boundary).
func applyLedgerMigrations(t *testing.T, dsn string) {
	t.Helper()
	m, err := migrate.New("file://../../services/ledger/migrations", dsn+"&x-migrations-table=schema_migrations_ledger")
	require.NoError(t, err)
	upErr := m.Up()
	_, _ = m.Close()
	if upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		require.NoError(t, upErr)
	}
}

func setupDatasetTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	const dbName, dbUser, dbPassword = "seev_test", "test", "secret"
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

	applyLedgerMigrations(t, dsn)

	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(ctx))
	return db
}

// seedAccounts inserts n 'user'/'cash' accounts, each with a funded balance,
// and enough balanced ledger_entries to exercise the counters loaddataset
// reports on — mirrors services/ledger/internal/ledger/schema_contract_test.go's own
// createUserCashAccount pattern, kept local since this tool has no
// dependency on services/ledger's repository package.
func seedAccounts(t *testing.T, db *sql.DB, n int) (totalBalance int64) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		var acctID string
		require.NoError(t, db.QueryRowContext(ctx,
			`INSERT INTO accounts (id, owner_id, owner_type, type, currency, status) VALUES (gen_random_uuid(), gen_random_uuid(), 'user', 'cash', 'IDR', 'active') RETURNING id`,
		).Scan(&acctID))
		balance := int64(1000 + i)
		_, err := db.ExecContext(ctx, `INSERT INTO account_balances (account_id, balance) VALUES ($1, $2)`, acctID, balance)
		require.NoError(t, err)
		totalBalance += balance

		var otherID string
		require.NoError(t, db.QueryRowContext(ctx,
			`INSERT INTO accounts (id, owner_id, owner_type, type, currency, status) VALUES (gen_random_uuid(), gen_random_uuid(), 'user', 'cash', 'IDR', 'active') RETURNING id`,
		).Scan(&otherID))
		_, err = db.ExecContext(ctx, `INSERT INTO account_balances (account_id, balance) VALUES ($1, 0)`, otherID)
		require.NoError(t, err)

		var txID string
		require.NoError(t, db.QueryRowContext(ctx,
			`INSERT INTO ledger_transactions (id, idempotency_key, type, status, amount, currency, source_account_id, destination_account_id)
			 VALUES (gen_random_uuid(), $1, 'transfer_p2p', 'posted', $2, 'IDR', $3, $4) RETURNING id`,
			fmt.Sprintf("manifest-test-%d", i), balance, otherID, acctID,
		).Scan(&txID))
		_, err = db.ExecContext(ctx,
			`INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, balance_after) VALUES ($1, $2, 'debit', $3, 0), ($1, $4, 'credit', $3, $3)`,
			txID, otherID, balance, acctID)
		require.NoError(t, err)
	}
	return totalBalance
}

func TestCollect_CountsAndBalanceMatchSeededData(t *testing.T) {
	db := setupDatasetTestDB(t)
	wantBalance := seedAccounts(t, db, 5)

	m := collect(context.Background(), db, "", "run-1")
	require.Empty(t, m.Errors)
	require.Equal(t, int64(10), m.UserAccountCount, "5 funded + 5 counterparty accounts")
	require.Equal(t, int64(5), m.LedgerTransactionCount)
	require.Equal(t, int64(10), m.LedgerEntryCount, "2 entries per transaction")
	require.Equal(t, fmt.Sprintf("%d", wantBalance), m.BalanceByCurrency["IDR"])
	require.NotEmpty(t, m.ContentHash)
	require.Equal(t, "unchecked", m.TierConformance, "no -tier given")
}

func TestCollect_ContentHashDeterministic(t *testing.T) {
	db := setupDatasetTestDB(t)
	seedAccounts(t, db, 3)

	firstManifest := collect(context.Background(), db, "", "run-a")
	secondManifest := collect(context.Background(), db, "", "run-b")
	require.Equal(t, firstManifest.ContentHash, secondManifest.ContentHash, "identical dataset shape must hash identically regardless of run_id")
}

func TestCollect_TierConformance_PassAndFail(t *testing.T) {
	db := setupDatasetTestDB(t)
	seedAccounts(t, db, 3)

	failing := collect(context.Background(), db, "D1", "run-1")
	require.Equal(t, "fail", failing.TierConformance)
	require.NotEmpty(t, failing.TierConformanceDetail)

	tiers["TINY"] = tierBounds{Label: "test tier", MinAccounts: 1, MaxAccounts: 100, MinLedgerEntries: 1, MaxLedgerEntries: 100}
	t.Cleanup(func() { delete(tiers, "TINY") })
	passing := collect(context.Background(), db, "TINY", "run-1")
	require.Equal(t, "pass", passing.TierConformance)
	require.Empty(t, passing.TierConformanceDetail)
}
