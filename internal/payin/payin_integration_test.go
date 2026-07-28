//go:build integration

// Package payin_test drives the normalized VendorService callback contract end
// to end against a real ledger and Postgres.
package payin_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/payin"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/internal/vendorboundary"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/database"
)

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}

func setupPayinTestDB(t *testing.T) *database.DBSQL {
	t.Helper()
	ctx := context.Background()
	const dbName, dbUser, dbPassword = "seev_test", "test", "secret"
	container, err := postgres.Run(ctx, "postgres:16.14-alpine", postgres.WithDatabase(dbName), postgres.WithUsername(dbUser), postgres.WithPassword(dbPassword), postgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5432")
	require.NoError(t, err)
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPassword, host, port.Port(), dbName)
	require.NoError(t, testutil.ApplyServiceMigrations(migrationsSourceURL(t), dsn))
	db, err := database.New(ctx, config.PostgresConfig{Host: host, Port: port.Port(), User: dbUser, Password: dbPassword, DB: dbName, SSLMode: "disable", MaxOpenConns: 10}.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func createUserCashAccount(t *testing.T, db *database.DBSQL, userID uuid.UUID) uuid.UUID {
	t.Helper()
	accountID := uuid.New()
	_, err := db.ExecContext(context.Background(), `INSERT INTO accounts (id, owner_id, owner_type, type, currency, status, created_by) VALUES ($1, $2, 'user', 'cash', 'IDR', 'active', 'payin_integration_test')`, accountID, userID)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), `INSERT INTO account_balances (account_id) VALUES ($1)`, accountID)
	require.NoError(t, err)
	return accountID
}

func getBalance(t *testing.T, db *database.DBSQL, accountID uuid.UUID) decimal.Decimal {
	t.Helper()
	var balance int64
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT balance FROM account_balances WHERE account_id = $1`, accountID).Scan(&balance))
	return decimal.NewFromInt(balance)
}

func newPayinModule(db *database.DBSQL) *payin.Module {
	ledgerModule := testutil.NewLedgerHarness(db)
	registry := vendorgw.NewRegistry()
	registry.AddPayin(vendorboundary.NewPayinAvailability("mockvendor"))
	return payin.NewModule(db, ledgerModule, registry, 0, nil, nil, nil, payinCryptoxTestRing)
}

func TestPayin_CreateTopupIntent_UsesDatabaseRoutingRule(t *testing.T) {
	db := setupPayinTestDB(t)
	ledgerModule := testutil.NewLedgerHarness(db)
	registry := vendorgw.NewRegistry()
	registry.AddPayin(vendorboundary.NewPayinAvailability("priorityvendor"))
	m := payin.NewModule(db, ledgerModule, registry, 0, nil, nil, nil, payinCryptoxTestRing)
	userID := uuid.New()
	createUserCashAccount(t, db, userID)
	_, err := db.ExecContext(context.Background(), `INSERT INTO payin_vendor_gateways (vendor, gateway) VALUES ('priorityvendor', 'gopay')`)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), `INSERT INTO payin_routing_rules (id, flow, priority, currency, min_amount, max_amount, vendor) VALUES ($1, 'topup', 10, 'IDR', 100000, 300000, 'priorityvendor')`, uuid.New())
	require.NoError(t, err)
	intent, err := m.CreateTopupIntent(context.Background(), userID, decimal.NewFromInt(250_000))
	require.NoError(t, err)
	assert.Equal(t, "priorityvendor", intent.Vendor)
}

func TestPayin_NormalizedCallback_IsIdempotent(t *testing.T) {
	db := setupPayinTestDB(t)
	m := newPayinModule(db)
	ctx := context.Background()
	userID := uuid.New()
	cash := createUserCashAccount(t, db, userID)
	intent, err := m.CreateTopupIntent(ctx, userID, decimal.NewFromInt(250_000))
	require.NoError(t, err)

	call := func() error {
		_, callErr := m.HandleVendorCallback(ctx, intent.Vendor, "evt-normalized-1", intent.Reference, "250000", "IDR", "settled", "2026-07-13T00:00:00Z", "inbox-1", "req-1", "")
		return callErr
	}
	require.NoError(t, call())
	require.NoError(t, call())
	assert.True(t, getBalance(t, db, cash).Equal(decimal.NewFromInt(250_000)))

	var eventCount, txCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM payin_webhook_events WHERE vendor_event_id = 'evt-normalized-1'`).Scan(&eventCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_transactions WHERE idempotency_key = 'payin:`+intent.Vendor+`:evt-normalized-1'`).Scan(&txCount))
	assert.Equal(t, 1, eventCount)
	assert.Equal(t, 1, txCount)

}
