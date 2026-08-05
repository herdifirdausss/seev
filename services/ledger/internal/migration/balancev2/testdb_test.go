//go:build integration

package balancev2

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/platform/config"
	"github.com/herdifirdausss/seev/internal/platform/database"
)

// applyLedgerMigrations applies services/ledger/migrations/ to the given
// database URL. It cannot use testkit.ApplyServiceMigrations because testkit
// also imports services/ledger, which in turn imports this package.
func applyLedgerMigrations(t *testing.T, dsn string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// balancev2/ → migration/ → internal/ → ledger/ → services/ → repo root
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "..")
	migrationsPath := filepath.Join(repoRoot, "services", "ledger", "migrations")

	migrationDSN := dsn + "?sslmode=disable&x-migrations-table=schema_migrations_ledger"
	m, err := migrate.New("file://"+migrationsPath, migrationDSN)
	require.NoError(t, err, "create ledger migrator")
	upErr := m.Up()
	_, _ = m.Close()
	if upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		require.NoError(t, upErr, "apply ledger migrations")
	}
}

// setupBalanceV2TestDB starts a throwaway Postgres container, applies all
// ledger migrations (including 000041 that creates account_balances_v2 and
// the migration control tables), and returns a connected *database.DBSQL.
func setupBalanceV2TestDB(t *testing.T) *database.DBSQL {
	t.Helper()
	ctx := context.Background()

	const dbName, dbUser, dbPassword = "seev_test", "test", "secret"

	container, err := postgres.Run(ctx,
		"postgres:16.14-alpine",
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

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s",
		dbUser, dbPassword, host, port.Port(), dbName)

	applyLedgerMigrations(t, dsn)

	cfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 20,
	}
	db, err := database.New(ctx, cfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

// seedAccount inserts one account + its initial v1 balance projection. Uses
// owner_type=system so the CHECK constraint (system ↔ owner_id IS NULL) is
// satisfied without a real user identity. Returns the new account ID.
func seedAccount(t *testing.T, db *database.DBSQL, accountType, currency string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO accounts (id, owner_type, type, currency, status, created_by)
		VALUES ($1, 'system', $2, $3, 'active', 'test')`,
		id, accountType, currency)
	require.NoError(t, err)

	_, err = db.ExecContext(context.Background(), `
		INSERT INTO account_balances (account_id, balance, allow_negative)
		VALUES ($1, 0, false)`,
		id)
	require.NoError(t, err)

	return id
}

// seedAccountWithBalance inserts an account whose v1 balance projection holds
// the given balance. The version trigger fires on the subsequent UPDATE.
func seedAccountWithBalance(t *testing.T, db *database.DBSQL, accountType, currency string, balance int64) uuid.UUID {
	t.Helper()
	id := seedAccount(t, db, accountType, currency)
	adjustBalance(t, db, id, balance)
	return id
}

// adjustBalance runs a direct UPDATE on account_balances, firing the BEFORE
// UPDATE trigger that increments the version column.
func adjustBalance(t *testing.T, db *database.DBSQL, accountID uuid.UUID, balance int64) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		UPDATE account_balances SET balance = $1 WHERE account_id = $2`,
		balance, accountID)
	require.NoError(t, err)
}

// postLedgerEntry inserts one ledger_transaction + one ledger_entry and bumps
// account_balances to balanceAfter (triggering the version increment). It
// simulates a real posting without needing the full ledger service stack.
// Returns the transaction ID.
func postLedgerEntry(t *testing.T, db *database.DBSQL, accountID uuid.UUID, amount, balanceAfter int64) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	txID := uuid.New()

	_, err := db.ExecContext(ctx, `
		INSERT INTO ledger_transactions
			(id, idempotency_key, idempotency_scope, type, status, amount, currency,
			 source_account_id, destination_account_id)
		SELECT $1, $2, 'test', 'money_in', 'posted', $3, a.currency, $4, $4
		FROM accounts a WHERE a.id = $4`,
		txID, uuid.New(), amount, accountID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transaction_id, account_id, direction, amount, balance_after)
		VALUES (gen_random_uuid(), $1, $2, 'credit', $3, $4)`,
		txID, accountID, amount, balanceAfter)
	require.NoError(t, err)

	// Updating account_balances fires the version trigger.
	_, err = db.ExecContext(ctx, `
		UPDATE account_balances SET balance = $1 WHERE account_id = $2`,
		balanceAfter, accountID)
	require.NoError(t, err)

	return txID
}

// sourceRowFor reads account_balances joined with accounts and returns a
// SourceRow ready for Transform / CompareRows — the same shape the backfill
// worker's keyset query produces.
func sourceRowFor(t *testing.T, db *database.DBSQL, accountID uuid.UUID) SourceRow {
	t.Helper()
	var row SourceRow
	err := db.QueryRowContext(context.Background(), `
		SELECT ab.account_id, a.currency, a.type, ab.balance, ab.allow_negative,
		       ab.version, ab.updated_at
		FROM account_balances ab
		JOIN accounts a ON a.id = ab.account_id
		WHERE ab.account_id = $1`, accountID,
	).Scan(&row.AccountID, &row.Currency, &row.AccountType,
		&row.Balance, &row.AllowNegative, &row.SourceVersion, &row.UpdatedAt)
	require.NoError(t, err)
	return row
}
