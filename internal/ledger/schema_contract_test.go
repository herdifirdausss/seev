//go:build integration

// Package ledger_test proves the canonical migrations in migrations/ match
// what the Go code in internal/ledger actually reads and writes (docs/plan/04
// Task 1a.5). It runs the real migrations against a throwaway Postgres
// container, then drives Service.Handle end-to-end through real (non-mock)
// repositories: money_in -> transfer_p2p -> money_out.
package ledger_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/internal/ledger/feepolicy"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/internal/ledger/service/accrual"
	"github.com/herdifirdausss/seev/internal/ledger/service/adjustments"
	"github.com/herdifirdausss/seev/internal/ledger/service/disbursement"
	ledgerhandle "github.com/herdifirdausss/seev/internal/ledger/service/handle"
	"github.com/herdifirdausss/seev/internal/ledger/service/provision"
	"github.com/herdifirdausss/seev/internal/ledger/service/recon"
	"github.com/herdifirdausss/seev/internal/ledger/service/schedule"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/database"
)

// migrationsSourceURL resolves migrations/ relative to this test file so the
// test works regardless of the working directory `go test` is invoked from.
func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}

// setupSchemaTestDB starts a throwaway Postgres container, applies every
// migrations/*.up.sql file via golang-migrate (the same mechanism `make
// migrate-up` uses), and returns a connected *database.DBSQL plus cleanup.
func setupSchemaTestDB(t *testing.T) *database.DBSQL {
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
		DB: dbName, SSLMode: "disable", MaxOpenConns: 20,
	}
	db, err := database.New(ctx, cfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

// appServiceTestDB bundles the two connections
// TestSchemaContract_AppServiceRole_* needs: ownerDB (schema owner —
// migrations already ran on this connection) and appDB (a fresh LOGIN role
// granted ONLY app_service, docs/plan/16 Task T3) plus appReadonlyDB (a
// separate LOGIN role granted ONLY app_readonly, for the negative tests).
type appServiceTestDB struct {
	ownerDB       *database.DBSQL
	appDB         *database.DBSQL
	appReadonlyDB *database.DBSQL
}

// setupAppServiceTestDB is setupSchemaTestDB plus the docs/plan/16 Task T3
// role split: after migrations create the app_service/app_readonly DB
// roles, it provisions two throwaway LOGIN roles (one per group role) and
// returns connections through each — this is what actually proves the
// grants in migrations/000009_rls_roles.up.sql are sufficient for the
// application to function, not just that the SQL runs without syntax
// errors under a superuser connection.
func setupAppServiceTestDB(t *testing.T) appServiceTestDB {
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

	ownerCfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	}
	ownerDB, err := database.New(ctx, ownerCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = ownerDB.Close() })

	const appPassword, readonlyPassword = "app-test-pw", "readonly-test-pw"
	_, err = ownerDB.ExecContext(ctx, `CREATE ROLE test_app_service LOGIN PASSWORD '`+appPassword+`'`)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, `GRANT app_service TO test_app_service`)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, `CREATE ROLE test_app_readonly LOGIN PASSWORD '`+readonlyPassword+`'`)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, `GRANT app_readonly TO test_app_readonly`)
	require.NoError(t, err)

	appCfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: "test_app_service", Password: appPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	}
	appDB, err := database.New(ctx, appCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })

	readonlyCfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: "test_app_readonly", Password: readonlyPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	}
	readonlyDB, err := database.New(ctx, readonlyCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = readonlyDB.Close() })

	return appServiceTestDB{ownerDB: ownerDB, appDB: appDB, appReadonlyDB: readonlyDB}
}

// createUserCashAccount inserts a user 'cash' account + zero balance row
// directly via SQL, bypassing ledger.Module.ProvisionUser (not implemented
// until docs/plan/05) — acceptable for a schema contract test.
func createUserCashAccount(t *testing.T, db *database.DBSQL, userID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	accountID := uuid.New()

	_, err := db.ExecContext(ctx, `
		INSERT INTO accounts (id, owner_id, owner_type, type, currency, status, created_by)
		VALUES ($1, $2, 'user', 'cash', 'IDR', 'active', 'schema_contract_test')`,
		accountID, userID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `INSERT INTO account_balances (account_id) VALUES ($1)`, accountID)
	require.NoError(t, err)

	return accountID
}

// seedCreditEntry directly inserts a fake posted transaction + a single
// credit ledger_entries row at a controlled createdAt — used by the balance
// snapshot tests (docs/plan/15 Task T1) to simulate ledger activity spread
// across specific calendar days/times, which the normal posting engine has
// no way to backdate. Also updates account_balances.balance to
// balanceAfter. Note: account_balances.updated_at ends up as the REAL
// current time regardless of createdAt — trg_balances_ua (migrations/000001)
// unconditionally stamps now() on any UPDATE — so callers that need
// updated_at to reflect a specific historical moment can't get that via this
// helper; use "today" as createdAt if a test depends on freshness filtering.
func seedCreditEntry(t *testing.T, db *database.DBSQL, accountID uuid.UUID, amount, balanceAfter int64, createdAt time.Time) {
	t.Helper()
	ctx := context.Background()
	txID := uuid.New()

	_, err := db.ExecContext(ctx, `
		INSERT INTO ledger_transactions (id, idempotency_key, type, status, amount, currency, created_at, updated_at)
		VALUES ($1, $2, 'money_in', 'posted', $3, 'IDR', $4, $4)`,
		txID, "seed-"+txID.String(), amount, createdAt)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transaction_id, account_id, direction, amount, balance_after, created_at)
		VALUES ($1, $2, $3, 'credit', $4, $5, $6)`,
		uuid.New(), txID, accountID, amount, balanceAfter, createdAt)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx,
		`UPDATE account_balances SET balance = $1, updated_at = $2 WHERE account_id = $3`,
		balanceAfter, createdAt, accountID)
	require.NoError(t, err)
}

// newService wires the posting engine against real repositories (a real
// tx.ExecContext-based markFailed can't run against the mockDB{} used by
// internal/ledger/service/handle's own unit tests) — screening is no
// longer part of this pipeline at all (docs/plan/37): it moved to the
// transport layer, tested separately (internal/ledger/transport,
// pkg/fraudcheck).
func newService(db *database.DBSQL) (*ledgerhandle.Service, repository.AccountRepository) {
	accRepo := repository.NewAccountRepository(db)
	txRepo := repository.NewTransactionRepository(db)
	balRepo := repository.NewBalanceRepository(db)
	entryRepo := repository.NewEntryRepository(db)
	outboxRepo := repository.NewOutboxRepository(db)
	registry := processors.NewDefaultRegistry(accRepo, txRepo)
	svc := ledgerhandle.New(db, txRepo, balRepo, entryRepo, outboxRepo, registry, slog.Default(), decimal.Zero, feepolicy.New(db))
	return svc, accRepo
}

// newAdjustmentsService wires the maker-checker service (docs/plan/16 Task
// T1) against real repositories, reusing newService's posting engine as its
// Poster.
func newAdjustmentsService(db *database.DBSQL) (*adjustments.Service, repository.AccountRepository) {
	handleSvc, accRepo := newService(db)
	adjRepo := repository.NewPendingAdjustmentRepository(db)
	txRepo := repository.NewTransactionRepository(db)
	outboxRepo := repository.NewOutboxRepository(db)
	return adjustments.New(db, adjRepo, txRepo, outboxRepo, handleSvc), accRepo
}

// newReconService wires the reconciliation service (docs/plan/16 Task T2)
// against real repositories, reusing a fresh adjustments.Service (itself
// backed by newService's posting engine) as its AdjustmentCreator — this
// makes ResolveItem in these tests go through the exact same maker-checker
// path a resolve request does in production.
func newReconService(db *database.DBSQL) (*recon.Service, *adjustments.Service, repository.AccountRepository) {
	adjSvc, accRepo := newAdjustmentsService(db)
	reconRepo := repository.NewReconRepository(db)
	return recon.New(db, reconRepo, adjSvc), adjSvc, accRepo
}

// newScheduleService wires the scheduled-transaction service (docs/plan/19
// Task T1) against real repositories, reusing newService's posting engine
// as its Poster.
func newScheduleService(db *database.DBSQL) (*schedule.Service, repository.ScheduledTransactionRepository) {
	handleSvc, _ := newService(db)
	scheduleRepo := repository.NewScheduledTransactionRepository(db)
	return schedule.New(db, scheduleRepo, handleSvc, slog.Default()), scheduleRepo
}

// newDisbursementService wires the batch disbursement service (docs/plan/19
// Task T2) against real repositories, reusing newService's posting engine
// as its Poster. maxPerRun overrides the production default (500) so tests
// can prove multi-call pagination without importing hundreds of rows.
func newDisbursementService(db *database.DBSQL, maxPerRun int) (*disbursement.Service, repository.DisbursementRepository) {
	handleSvc, _ := newService(db)
	txRepo := repository.NewTransactionRepository(db)
	disbursementRepo := repository.NewDisbursementRepository(db)
	return disbursement.New(db, disbursementRepo, txRepo, handleSvc, disbursement.WithMaxItemsPerRun(maxPerRun)), disbursementRepo
}

// newAccrualService wires the interest accrual service (docs/plan/19 Task
// T3) against real repositories, reusing newService's posting engine as
// its Poster and a real SnapshotRepository as its (snapshot-only) balance
// basis.
func newAccrualService(db *database.DBSQL) (*accrual.Service, repository.SavingsRepository) {
	handleSvc, _ := newService(db)
	savingsRepo := repository.NewSavingsRepository(db)
	snapshotRepo := repository.NewSnapshotRepository(db, time.UTC)
	return accrual.New(db, savingsRepo, snapshotRepo, handleSvc, slog.Default()), savingsRepo
}

func getBalance(t *testing.T, db *database.DBSQL, accountID uuid.UUID) decimal.Decimal {
	t.Helper()
	var balance int64
	err := db.QueryRowContext(context.Background(),
		`SELECT balance FROM account_balances WHERE account_id = $1`, accountID).Scan(&balance)
	require.NoError(t, err)
	return decimal.NewFromInt(balance)
}

func countLedgerTransactions(t *testing.T, db *database.DBSQL, idempotencyKey string) int {
	t.Helper()
	var count int
	err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM ledger_transactions WHERE idempotency_key = $1`, idempotencyKey).Scan(&count)
	require.NoError(t, err)
	return count
}

// getSourceDest reads back source_account_id/destination_account_id for the
// transaction with the given idempotency key — used to prove they hold the
// semantically correct account (docs/plan/14 Task T1), not just "some
// non-NULL UUID".
func getSourceDest(t *testing.T, db *database.DBSQL, idempotencyKey string) (source, dest uuid.UUID) {
	t.Helper()
	var srcStr, destStr *string
	err := db.QueryRowContext(context.Background(),
		`SELECT source_account_id, destination_account_id FROM ledger_transactions WHERE idempotency_key = $1`,
		idempotencyKey,
	).Scan(&srcStr, &destStr)
	require.NoError(t, err)
	if srcStr != nil {
		source = uuid.MustParse(*srcStr)
	}
	if destStr != nil {
		dest = uuid.MustParse(*destStr)
	}
	return source, dest
}

// TestSchemaContract_EndToEndFlow drives money_in -> transfer_p2p -> money_out
// through the real posting engine and real repositories, then proves the
// ledger's own integrity-verification functions see zero discrepancies.
func TestSchemaContract_EndToEndFlow(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, accRepo := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	userB := uuid.New()
	cashA := createUserCashAccount(t, db, userA)
	cashB := createUserCashAccount(t, db, userB)

	// 1. money_in: 100_000 IDR into userA's cash account via "bca" settlement.
	err := svc.Handle(ctx, processors.Command{
		IdempotencyKey: "topup-1",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(100_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	})
	require.NoError(t, err)
	require.True(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(100_000)))

	// [docs/plan/14 Task T1] source/destination must be the semantically
	// correct accounts (settlement debited, user cash credited) — not just
	// non-NULL.
	bcaSettlement, err := accRepo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, "bca", "IDR")
	require.NoError(t, err)
	src, dest := getSourceDest(t, db, "topup-1")
	require.Equal(t, bcaSettlement, src, "money_in source must be the settlement account")
	require.Equal(t, cashA, dest, "money_in destination must be the credited user account")

	// 2. transfer_p2p: userA -> userB, 30_000 IDR.
	err = svc.Handle(ctx, processors.Command{
		IdempotencyKey: "transfer-1",
		Type:           "transfer_p2p",
		Amount:         decimal.NewFromInt(30_000),
		UserID:         userA,
		TargetUserID:   userB,
	})
	require.NoError(t, err)
	require.True(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(70_000)))
	require.True(t, getBalance(t, db, cashB).Equal(decimal.NewFromInt(30_000)))

	src, dest = getSourceDest(t, db, "transfer-1")
	require.Equal(t, cashA, src, "transfer_p2p source must be the sender")
	require.Equal(t, cashB, dest, "transfer_p2p destination must be the receiver")

	// 3. money_out: userB withdraws 10_000 IDR via "gopay" settlement.
	err = svc.Handle(ctx, processors.Command{
		IdempotencyKey: "withdraw-1",
		Type:           "money_out",
		Amount:         decimal.NewFromInt(10_000),
		UserID:         userB,
		Metadata:       map[string]any{"gateway": "gopay"},
	})
	require.NoError(t, err)
	require.True(t, getBalance(t, db, cashB).Equal(decimal.NewFromInt(20_000)))

	gopaySettlement, err := accRepo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, "gopay", "IDR")
	require.NoError(t, err)
	src, dest = getSourceDest(t, db, "withdraw-1")
	require.Equal(t, cashB, src, "money_out source must be the debited user account")
	require.Equal(t, gopaySettlement, dest, "money_out destination must be the settlement account")

	// Invariant #1: no transaction has debit != credit.
	rows, err := db.QueryContext(ctx, `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity')`)
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next(), "fn_verify_ledger_balance found an unbalanced transaction")
	require.NoError(t, rows.Err())

	// Invariant #2: stored balance == balance computed from entries, for
	// every account touched by this test.
	for _, accID := range []uuid.UUID{cashA, cashB} {
		var isConsistent bool
		var diff int64
		err := db.QueryRowContext(ctx,
			`SELECT is_consistent, diff FROM fn_verify_account_balance($1)`, accID,
		).Scan(&isConsistent, &diff)
		require.NoError(t, err)
		require.True(t, isConsistent, "account %s inconsistent, diff=%d", accID, diff)
	}
}

// TestSchemaContract_OutboxEventContract proves the versioned event contract
// (docs/plan/14 Task T3) end-to-end: post a real money_in, read the RAW
// payload back out of outbox_events, unmarshal it into events.TransactionPosted
// (not a generic map), and check the fields match the actual posting —
// proof the wire format is exactly what the events package claims, not just
// what a unit test mock returned.
func TestSchemaContract_OutboxEventContract(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "topup-events-1",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(75_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca", "external_ref": "ext-abc-123"},
	}))

	var txID uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM ledger_transactions WHERE idempotency_key = $1`, "topup-events-1",
	).Scan(&txID))

	var eventType string
	var rawPayload []byte
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT event_type, payload FROM outbox_events WHERE aggregate_id = $1`, txID,
	).Scan(&eventType, &rawPayload))

	require.Equal(t, events.TypeTransactionPosted, eventType)

	var payload events.TransactionPosted
	require.NoError(t, json.Unmarshal(rawPayload, &payload))

	assert.Equal(t, 1, payload.SchemaVersion)
	assert.Equal(t, txID, payload.TxID)
	assert.Equal(t, "money_in", payload.TransactionType)
	assert.Equal(t, "75000", payload.Amount)
	assert.Equal(t, "IDR", payload.Currency)
	require.NotNil(t, payload.DestinationAccountID)
	assert.Equal(t, cashA, *payload.DestinationAccountID)
	assert.Equal(t, "ext-abc-123", payload.ExternalRef)
	require.Len(t, payload.Entries, 2)
	assert.Equal(t, "debit", payload.Entries[0].Direction)
	assert.Equal(t, "credit", payload.Entries[1].Direction)
	assert.Equal(t, cashA, payload.Entries[1].AccountID)
	assert.Equal(t, "75000", payload.Entries[1].Amount)
	assert.False(t, payload.OccurredAt.IsZero())
}

// TestSchemaContract_Idempotency proves that replaying the same command with
// the same idempotency key never creates a second ledger_transactions row and
// never double-posts the balance.
func TestSchemaContract_Idempotency(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)

	cmd := processors.Command{
		IdempotencyKey: "idem-topup-1",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(50_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}

	require.NoError(t, svc.Handle(ctx, cmd))
	require.NoError(t, svc.Handle(ctx, cmd)) // replay — must be a no-op success, not an error

	require.Equal(t, 1, countLedgerTransactions(t, db, cmd.IdempotencyKey))
	require.True(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(50_000)), "balance must not double-post")
}

// TestSchemaContract_ReversalSourceDestAlwaysNull proves Reversal never
// writes a source/destination account (decision K2, docs/plan/13/14 Task
// T1): a reversal can touch more than the original's two legs (fee-bearing
// transactions), so there's no single semantic source->destination pair.
func TestSchemaContract_ReversalSourceDestAlwaysNull(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)

	err := svc.Handle(ctx, processors.Command{
		IdempotencyKey: "topup-rev-1",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(50_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	})
	require.NoError(t, err)

	var originalTxID uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM ledger_transactions WHERE idempotency_key = $1`, "topup-rev-1",
	).Scan(&originalTxID))

	err = svc.Handle(ctx, processors.Command{
		IdempotencyKey: "reverse-1",
		Type:           "reversal",
		Amount:         decimal.NewFromInt(50_000),
		UserID:         userA,
		ReferenceID:    originalTxID,
	})
	require.NoError(t, err)
	require.True(t, getBalance(t, db, cashA).IsZero())

	src, dest := getSourceDest(t, db, "reverse-1")
	require.Equal(t, uuid.Nil, src, "reversal must leave source_account_id NULL")
	require.Equal(t, uuid.Nil, dest, "reversal must leave destination_account_id NULL")
}

// TestSchemaContract_ConcurrentReversal_NoDoubleClose proves the fix for
// finding N1 (docs/plan/13): under the OLD code (SELECT status, then a plain
// UPDATE with no WHERE guard), two concurrent reversal requests for the same
// original transaction could both observe status='posted' and both post a
// reversal, doubling the credit. With CloseOriginal's atomic conditional
// UPDATE, exactly one of N concurrent reversal attempts may succeed.
func TestSchemaContract_ConcurrentReversal_NoDoubleClose(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "topup-race-1",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(100_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	var originalTxID uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM ledger_transactions WHERE idempotency_key = $1`, "topup-race-1",
	).Scan(&originalTxID))

	const attempts = 10
	results := make([]error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = svc.Handle(ctx, processors.Command{
				IdempotencyKey: fmt.Sprintf("reverse-race-%d", i),
				Type:           "reversal",
				Amount:         decimal.NewFromInt(100_000),
				UserID:         userA,
				ReferenceID:    originalTxID,
			})
		}(i)
	}
	wg.Wait()

	// Losers can fail two different — both correct — ways depending on
	// timing: ErrAlreadyClosed is the actual race loser of the atomic
	// CloseOriginal UPDATE (two requests both passed Validate's unlocked
	// read and only one won the UPDATE); ErrAlreadyReversed is Validate's
	// own fast-fail for a request that arrived after the winner had already
	// committed. Under the OLD code (plain UPDATE, no WHERE guard) this test
	// would show successCount > 1 — that's the bug this task fixes.
	successCount := 0
	for _, err := range results {
		if err == nil {
			successCount++
		} else {
			require.True(t, errors.Is(err, apperror.ErrAlreadyClosed) || errors.Is(err, apperror.ErrAlreadyReversed),
				"non-winning reversal must fail with ErrAlreadyClosed or ErrAlreadyReversed, got: %v", err)
		}
	}
	require.Equal(t, 1, successCount, "exactly one of %d concurrent reversals must win the race", attempts)

	// Balance must reflect exactly ONE reversal (back to zero), not N.
	require.True(t, getBalance(t, db, cashA).IsZero(),
		"balance must be zero after exactly one reversal, not double/triple-credited")

	rows, err := db.QueryContext(ctx, `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity')`)
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next(), "fn_verify_ledger_balance found an unbalanced transaction after concurrent reversal race")
	require.NoError(t, rows.Err())
}

// TestSchemaContract_LifecycleGuard_SettleAfterCancel_Rejected proves finding
// N3 (docs/plan/13): once a withdraw_initiate has been cancelled,
// withdraw_settle against the same ReferenceID must be rejected — it can no
// longer silently consume hold funds that (in a multi-withdrawal scenario)
// would belong to a different, still-active withdrawal.
func TestSchemaContract_LifecycleGuard_SettleAfterCancel_Rejected(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	provisionErr := provisionStandardAccounts(t, db, userA)
	require.NoError(t, provisionErr)

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "topup-lg-1",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(50_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "wd-init-1",
		Type:           "withdraw_initiate",
		Amount:         decimal.NewFromInt(50_000),
		UserID:         userA,
	}))

	var initiateTxID uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM ledger_transactions WHERE idempotency_key = $1`, "wd-init-1",
	).Scan(&initiateTxID))

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "wd-cancel-1",
		Type:           "withdraw_cancel",
		Amount:         decimal.NewFromInt(50_000),
		UserID:         userA,
		ReferenceID:    initiateTxID,
	}))

	// Settle against the SAME initiate, after it was already cancelled.
	err := svc.Handle(ctx, processors.Command{
		IdempotencyKey: "wd-settle-1",
		Type:           "withdraw_settle",
		Amount:         decimal.NewFromInt(50_000),
		UserID:         userA,
		ReferenceID:    initiateTxID,
		Metadata:       map[string]any{"gateway": "bca"},
	})
	require.Error(t, err, "settle after cancel must be rejected")
	require.True(t, errors.Is(err, apperror.ErrAlreadyClosed), "expected ErrAlreadyClosed, got: %v", err)

	// Funds must be back in cash (from the cancel), not drained by the
	// rejected settle attempt.
	cashID, err := repository.NewAccountRepository(db).GetAccountID(ctx, userA, constant.AccountTypeCash)
	require.NoError(t, err)
	require.True(t, getBalance(t, db, cashID).Equal(decimal.NewFromInt(50_000)),
		"cash must hold the cancelled withdrawal's funds, untouched by the rejected settle")
}

// TestSchemaContract_LifecycleGuard_AmountMismatch_Rejected proves settling
// a withdraw_initiate with an amount different from the original is rejected
// — MVP supports full-amount settle only (decision K3, docs/plan/13).
func TestSchemaContract_LifecycleGuard_AmountMismatch_Rejected(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	require.NoError(t, provisionStandardAccounts(t, db, userA))

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "topup-lg-2",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(80_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "wd-init-2",
		Type:           "withdraw_initiate",
		Amount:         decimal.NewFromInt(80_000),
		UserID:         userA,
	}))

	var initiateTxID uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM ledger_transactions WHERE idempotency_key = $1`, "wd-init-2",
	).Scan(&initiateTxID))

	err := svc.Handle(ctx, processors.Command{
		IdempotencyKey: "wd-settle-2",
		Type:           "withdraw_settle",
		Amount:         decimal.NewFromInt(40_000), // half — not full amount
		UserID:         userA,
		ReferenceID:    initiateTxID,
		Metadata:       map[string]any{"gateway": "bca"},
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, apperror.ErrLifecycleAmountMismatch), "expected ErrLifecycleAmountMismatch, got: %v", err)
}

// TestSchemaContract_LifecycleGuard_DoubleSettle_Rejected proves settling the
// same withdraw_initiate twice is rejected — direct coverage of the
// "initiate -> settle -> settle again" scenario named in docs/plan/13's N3.
func TestSchemaContract_LifecycleGuard_DoubleSettle_Rejected(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	require.NoError(t, provisionStandardAccounts(t, db, userA))

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "topup-lg-3",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(60_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))
	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "wd-init-3",
		Type:           "withdraw_initiate",
		Amount:         decimal.NewFromInt(60_000),
		UserID:         userA,
	}))

	var initiateTxID uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM ledger_transactions WHERE idempotency_key = $1`, "wd-init-3",
	).Scan(&initiateTxID))

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "wd-settle-3a",
		Type:           "withdraw_settle",
		Amount:         decimal.NewFromInt(60_000),
		UserID:         userA,
		ReferenceID:    initiateTxID,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	// Second settle against the SAME initiate, with a different idempotency
	// key (a genuine second attempt, not a retry of the first).
	err := svc.Handle(ctx, processors.Command{
		IdempotencyKey: "wd-settle-3b",
		Type:           "withdraw_settle",
		Amount:         decimal.NewFromInt(60_000),
		UserID:         userA,
		ReferenceID:    initiateTxID,
		Metadata:       map[string]any{"gateway": "bca"},
	})
	require.Error(t, err, "double-settle must be rejected")
	require.True(t, errors.Is(err, apperror.ErrAlreadyClosed), "expected ErrAlreadyClosed, got: %v", err)

	rows, err := db.QueryContext(ctx, `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity')`)
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next(), "fn_verify_ledger_balance found an unbalanced transaction after rejected double-settle")
	require.NoError(t, rows.Err())
}

// provisionStandardAccounts creates the cash/hold/pending/frozen account set
// a real user needs (docs/plan/05 Task 1b.2's provisioning service),
// bypassing the ledger.Module facade — acceptable for a schema contract test
// that only needs the accounts to exist, not the HTTP surface.
func provisionStandardAccounts(t *testing.T, db *database.DBSQL, userID uuid.UUID) error {
	t.Helper()
	_, err := provision.New(db).CreateUserAccounts(context.Background(), userID, "IDR")
	return err
}

// TestSchemaContract_LedgerEntriesImmutable proves the DB trigger rejects any
// UPDATE to a posted ledger_entries row, regardless of application-layer
// intent — the append-only guarantee must hold even against a direct SQL
// UPDATE, not just through Go code paths.
func TestSchemaContract_LedgerEntriesImmutable(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	createUserCashAccount(t, db, userA)

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "immutable-topup-1",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(1_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	var entryID uuid.UUID
	err := db.QueryRowContext(ctx, `SELECT id FROM ledger_entries LIMIT 1`).Scan(&entryID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `UPDATE ledger_entries SET amount = 1 WHERE id = $1`, entryID)
	require.Error(t, err, "UPDATE on ledger_entries must be rejected by trg_entries_immutable")

	_, err = db.ExecContext(ctx, `DELETE FROM ledger_entries WHERE id = $1`, entryID)
	require.Error(t, err, "DELETE on ledger_entries must be rejected by trg_entries_immutable")
}

// TestSchemaContract_AccountRepository exercises every AccountRepository
// lookup against the real schema, including the not-found path.
func TestSchemaContract_AccountRepository(t *testing.T) {
	db := setupSchemaTestDB(t)
	_, accRepo := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)

	gotCash, err := accRepo.GetAccountID(ctx, userA, constant.AccountTypeCash)
	require.NoError(t, err)
	require.Equal(t, cashA, gotCash)

	currency, err := accRepo.GetAccountCurrency(ctx, cashA)
	require.NoError(t, err)
	require.Equal(t, "IDR", currency)

	settlementBCA, err := accRepo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, "bca", "IDR")
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, settlementBCA)

	_, err = accRepo.GetAccountID(ctx, uuid.New(), constant.AccountTypeCash)
	require.Error(t, err)
	require.True(t, errors.Is(err, apperror.ErrAccountNotFound))

	_, err = accRepo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, "nonexistent-gateway", "IDR")
	require.Error(t, err)
	require.True(t, errors.Is(err, apperror.ErrAccountNotFound))
}

// TestSchemaContract_ConcurrentSystemAccountDeltas_NoLostUpdate is the core
// proof for docs/plan/11 Task T1: settlement[bca] is never locked with
// FOR UPDATE (allow_negative=true — see ApplySystemDeltas), only updated via
// `balance = balance + delta`. Firing many concurrent money_in postings
// through the SAME settlement account and checking its final balance
// against the exact expected sum is what actually catches a lost update — a
// naive read-then-write (or a stale-snapshot bug in the split logic) would
// under-count here under real concurrency, whereas a single-goroutine test
// never would.
func TestSchemaContract_ConcurrentSystemAccountDeltas_NoLostUpdate(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, accRepo := newService(db)
	ctx := context.Background()

	settlementBCA, err := accRepo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, "bca", "IDR")
	require.NoError(t, err)

	const n = 50
	amounts := make([]int64, n)
	userIDs := make([]uuid.UUID, n)
	cashAccounts := make([]uuid.UUID, n)
	for i := 0; i < n; i++ {
		amounts[i] = int64(1_000 + i*137) // distinct, non-round amounts
		userIDs[i] = uuid.New()
		cashAccounts[i] = createUserCashAccount(t, db, userIDs[i])
	}

	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := svc.Handle(ctx, processors.Command{
				IdempotencyKey: fmt.Sprintf("concurrent-topup-%d", i),
				Type:           "money_in",
				Amount:         decimal.NewFromInt(amounts[i]),
				UserID:         userIDs[i],
				Metadata:       map[string]any{"gateway": "bca"},
			})
			if err != nil {
				errCh <- fmt.Errorf("goroutine %d: %w", i, err)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent money_in failed: %v", err)
	}
	require.Zero(t, len(errCh), "no goroutine should fail")

	var wantSum int64
	for _, a := range amounts {
		wantSum += a
	}

	// settlement is the SOURCE (debited) for money_in — its balance moves
	// further negative by exactly the sum of every concurrently-posted
	// amount. Starting balance is 0 in a fresh test container.
	gotSettlement := getBalance(t, db, settlementBCA)
	require.True(t, gotSettlement.Equal(decimal.NewFromInt(-wantSum)),
		"settlement[bca] balance = %s, want %s — a lost update occurred under concurrency",
		gotSettlement, decimal.NewFromInt(-wantSum))

	// Every user cash account must have received exactly its own amount —
	// proves the per-transaction atomicity held even while N transactions
	// raced on the shared settlement row.
	for i, accID := range cashAccounts {
		got := getBalance(t, db, accID)
		require.True(t, got.Equal(decimal.NewFromInt(amounts[i])),
			"cash account %d balance = %s, want %d", i, got, amounts[i])
	}

	// Ultimate invariant: zero unbalanced transactions across the whole run.
	rows, err := db.QueryContext(ctx, `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity')`)
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next(), "fn_verify_ledger_balance found an unbalanced transaction after concurrent run")
	require.NoError(t, rows.Err())

	var isConsistent bool
	var diff int64
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT is_consistent, diff FROM fn_verify_account_balance($1)`, settlementBCA,
	).Scan(&isConsistent, &diff))
	require.True(t, isConsistent, "settlement[bca] projection vs entries diverged, diff=%d", diff)
}

// ─── docs/plan/15 Task T1: balance snapshots ───────────────────────────────

func jakartaLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)
	return loc
}

// TestSchemaContract_BalanceSnapshot_MultiDay seeds entries across 3
// calendar days (Asia/Jakarta) — with a gap on the middle day — and proves:
// snapshots are written only for days with activity, GetLatestBefore walks
// back correctly across the gap, BalanceAsOf matches the running balance at
// each snapshotted date, and re-running InsertForDate for an already-done
// date is a no-op (idempotent).
func TestSchemaContract_BalanceSnapshot_MultiDay(t *testing.T) {
	db := setupSchemaTestDB(t)
	loc := jakartaLoc(t)
	repo := repository.NewSnapshotRepository(db, loc)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)

	day1 := time.Date(2026, 6, 1, 10, 0, 0, 0, loc)
	day2 := time.Date(2026, 6, 2, 10, 0, 0, 0, loc) // deliberately no activity
	day3 := time.Date(2026, 6, 3, 10, 0, 0, 0, loc)

	seedCreditEntry(t, db, cashA, 10_000, 10_000, day1)
	seedCreditEntry(t, db, cashA, 5_000, 15_000, day3)

	n1, err := repo.InsertForDate(ctx, day1)
	require.NoError(t, err)
	require.Equal(t, 1, n1)

	n2, err := repo.InsertForDate(ctx, day2)
	require.NoError(t, err)
	require.Equal(t, 0, n2, "day2 had no activity — must not write a row")

	n3, err := repo.InsertForDate(ctx, day3)
	require.NoError(t, err)
	require.Equal(t, 1, n3)

	// GetLatestBefore(day2) must walk back to day1's snapshot — no row
	// exists for day2 itself.
	bal, asOf, found, err := repo.GetLatestBefore(ctx, cashA, day2)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, bal.Equal(decimal.NewFromInt(10_000)))
	require.Equal(t, day1.Format("2006-01-02"), asOf.Format("2006-01-02"))

	// BalanceAsOf each snapshotted date matches the running balance.
	asOfDay1, err := repo.BalanceAsOf(ctx, cashA, day1)
	require.NoError(t, err)
	require.True(t, asOfDay1.Equal(decimal.NewFromInt(10_000)))

	asOfDay3, err := repo.BalanceAsOf(ctx, cashA, day3)
	require.NoError(t, err)
	require.True(t, asOfDay3.Equal(decimal.NewFromInt(15_000)))

	// Re-running for day1 is idempotent: no new row, no error, no double
	// counting.
	nAgain, err := repo.InsertForDate(ctx, day1)
	require.NoError(t, err)
	require.Equal(t, 0, nAgain, "re-running InsertForDate for an already-snapshotted date must not write again")

	var rowCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM account_balance_snapshots WHERE account_id = $1 AND as_of_date = $2::date`,
		cashA, day1.Format("2006-01-02"),
	).Scan(&rowCount))
	require.Equal(t, 1, rowCount, "must still be exactly one snapshot row for day1")
}

// TestSchemaContract_BalanceSnapshot_TimezoneBoundary proves an entry at
// 23:30 WIB and one at 00:30 WIB the next calendar day land in different
// snapshot dates — even though in UTC they're only ~1 hour apart on the SAME
// UTC calendar day, which is exactly the trap AT TIME ZONE arithmetic must
// avoid (docs/plan/15 Task T1).
func TestSchemaContract_BalanceSnapshot_TimezoneBoundary(t *testing.T) {
	db := setupSchemaTestDB(t)
	loc := jakartaLoc(t)
	repo := repository.NewSnapshotRepository(db, loc)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)

	day1 := time.Date(2026, 6, 10, 23, 30, 0, 0, loc) // 2026-06-10 23:30 WIB = 16:30 UTC same day
	day2 := time.Date(2026, 6, 11, 0, 30, 0, 0, loc)  // 2026-06-11 00:30 WIB = 17:30 UTC — SAME UTC day as above

	seedCreditEntry(t, db, cashA, 1_000, 1_000, day1)
	seedCreditEntry(t, db, cashA, 2_000, 3_000, day2)

	n1, err := repo.InsertForDate(ctx, day1)
	require.NoError(t, err)
	require.Equal(t, 1, n1)
	n2, err := repo.InsertForDate(ctx, day2)
	require.NoError(t, err)
	require.Equal(t, 1, n2)

	var entryCount1, entryCount2 int
	var closing1, closing2 int64
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT entry_count, closing_balance FROM account_balance_snapshots WHERE account_id=$1 AND as_of_date=$2::date`,
		cashA, "2026-06-10",
	).Scan(&entryCount1, &closing1))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT entry_count, closing_balance FROM account_balance_snapshots WHERE account_id=$1 AND as_of_date=$2::date`,
		cashA, "2026-06-11",
	).Scan(&entryCount2, &closing2))

	require.Equal(t, 1, entryCount1, "2026-06-10 (WIB) snapshot must count only its own entry")
	require.Equal(t, int64(1_000), closing1)
	require.Equal(t, 1, entryCount2, "2026-06-11 (WIB) snapshot must count only its own entry")
	require.Equal(t, int64(3_000), closing2)
}

// TestSchemaContract_BalanceSnapshot_MismatchDetected corrupts
// a snapshot's closing_balance was computed wrong while the account's
// account_balances row has had no activity since — proves VerifyDate
// catches the divergence.
//
// Two things this test deliberately works around, both stemming from
// account_balances' BEFORE UPDATE trigger (trg_balances_ua,
// migrations/000001) unconditionally setting updated_at=now() on ANY write:
//  1. seedCreditEntry's own UPDATE to account_balances always stamps the
//     REAL current time as updated_at, no matter what createdAt is passed —
//     so this test uses "today" (real now, Asia/Jakarta) as its snapshot
//     date instead of an arbitrary historical one, so VerifyDate's
//     freshness filter (updated_at < date+1) naturally includes it.
//  2. Corrupting account_balances directly (instead of the snapshot) would
//     ALSO re-stamp updated_at to now(), making the account look "active
//     after the snapshot" and get correctly excluded — which is accurate:
//     a balance touched by anything (even a bug) after the snapshot date
//     isn't distinguishable from legitimate activity by this mechanism.
//     Corrupting the snapshot row itself (no such trigger — snapshots are
//     rebuildable by design, decision K6) is what actually simulates "the
//     snapshot computation was wrong for an otherwise-untouched account",
//     which is the scenario VerifyDate exists to catch.
func TestSchemaContract_BalanceSnapshot_MismatchDetected(t *testing.T) {
	db := setupSchemaTestDB(t)
	loc := jakartaLoc(t)
	repo := repository.NewSnapshotRepository(db, loc)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)

	day1 := time.Now().In(loc)
	seedCreditEntry(t, db, cashA, 7_000, 7_000, day1)

	n, err := repo.InsertForDate(ctx, day1)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// Consistent so far — VerifyDate must find nothing.
	clean, err := repo.VerifyDate(ctx, day1)
	require.NoError(t, err)
	require.Empty(t, clean)

	// Corrupt the SNAPSHOT (not account_balances — see comment above).
	_, err = db.ExecContext(ctx,
		`UPDATE account_balance_snapshots SET closing_balance = $1 WHERE account_id = $2 AND as_of_date = $3::date`,
		999_999, cashA, day1.Format("2006-01-02"))
	require.NoError(t, err)

	mismatches, err := repo.VerifyDate(ctx, day1)
	require.NoError(t, err)
	require.Len(t, mismatches, 1)
	require.Equal(t, cashA, mismatches[0].AccountID)
	require.True(t, mismatches[0].SnapshotBalance.Equal(decimal.NewFromInt(999_999)))
	require.True(t, mismatches[0].CurrentBalance.Equal(decimal.NewFromInt(7_000)))
}

// TestSchemaContract_BalanceSnapshot_LatestDate_EmptyTable is a regression
// test for a real bug the docs/plan/15 Task T1 Docker smoke test caught:
// `SELECT max(as_of_date)` over an EMPTY table returns one row with a NULL
// value, not zero rows — sql.ErrNoRows never fires, and scanning straight
// into *time.Time panicked with "unsupported Scan ... storing driver.Value
// type <nil>". SnapshotJob.catchUp calls this on every process start, so a
// fresh deployment with no prior snapshots would have errored on every
// single startup. sql.NullTime is the fix.
func TestSchemaContract_BalanceSnapshot_LatestDate_EmptyTable(t *testing.T) {
	db := setupSchemaTestDB(t)
	loc := jakartaLoc(t)
	repo := repository.NewSnapshotRepository(db, loc)
	ctx := context.Background()

	_, found, err := repo.LatestSnapshotDate(ctx)
	require.NoError(t, err, "must not error on an empty account_balance_snapshots table")
	require.False(t, found)

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)
	day1 := time.Now().In(loc)
	seedCreditEntry(t, db, cashA, 1_000, 1_000, day1)
	_, err = repo.InsertForDate(ctx, day1)
	require.NoError(t, err)

	date, found, err := repo.LatestSnapshotDate(ctx)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, day1.Format("2006-01-02"), date.Format("2006-01-02"))
}

// ─── docs/plan/15 Task T2: statement (opening/closing + entries) ──────────

// TestSchemaContract_Statement_PeriodOpeningClosing exercises the exact
// repository composition Module.Statement uses — SnapshotRepository.
// BalanceAsOf for the opening balance, EntryRepository.ListByAccountRange
// for the period's entries — against real Postgres, seeding activity across
// 3 days and requesting a statement for the LAST TWO of them (so opening
// balance must come from a snapshot of day1, not a replay from account
// creation).
func TestSchemaContract_Statement_PeriodOpeningClosing(t *testing.T) {
	db := setupSchemaTestDB(t)
	loc := jakartaLoc(t)
	snapshotRepo := repository.NewSnapshotRepository(db, loc)
	entryRepo := repository.NewEntryRepository(db)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)

	day1 := time.Date(2026, 5, 1, 10, 0, 0, 0, loc)
	day2 := time.Date(2026, 5, 2, 10, 0, 0, 0, loc)
	day3 := time.Date(2026, 5, 3, 10, 0, 0, 0, loc)

	seedCreditEntry(t, db, cashA, 10_000, 10_000, day1) // opening baseline
	seedCreditEntry(t, db, cashA, 3_000, 13_000, day2)  // in-period
	seedCreditEntry(t, db, cashA, 2_000, 15_000, day3)  // in-period

	_, err := snapshotRepo.InsertForDate(ctx, day1)
	require.NoError(t, err)

	// Statement period = [day2, day3] — opening balance must come from
	// day1's snapshot (the day BEFORE the period), not a full replay back
	// to account creation.
	opening, err := snapshotRepo.BalanceAsOf(ctx, cashA, day1)
	require.NoError(t, err)
	require.True(t, opening.Equal(decimal.NewFromInt(10_000)), "opening balance must equal day1's closing balance")

	entries, err := entryRepo.ListByAccountRange(ctx, cashA, day2, day3, loc, 100)
	require.NoError(t, err)
	require.Len(t, entries, 2, "period must contain exactly day2 and day3's entries, not day1's")

	// Chronological order (statements read top-to-bottom by date).
	require.True(t, entries[0].CreatedAt.Before(entries[1].CreatedAt))
	require.Equal(t, "money_in", entries[0].TransactionType)
	require.True(t, entries[0].Amount.Equal(decimal.NewFromInt(3_000)))
	require.True(t, entries[0].BalanceAfter.Equal(decimal.NewFromInt(13_000)))
	require.True(t, entries[1].Amount.Equal(decimal.NewFromInt(2_000)))

	closing := entries[len(entries)-1].BalanceAfter
	require.True(t, closing.Equal(decimal.NewFromInt(15_000)), "closing balance must equal the last entry's balance_after")
}

// TestSchemaContract_Statement_RangeTooLarge_LimitPlusOne proves
// ListByAccountRange's LIMIT+1 convention actually detects "too many rows"
// — seed maxRows+1 entries in one day, request with limit=maxRows+1, and
// confirm all maxRows+1 rows come back (the caller, Module.Statement, is
// what turns "more than maxStatementEntries" into
// apperror.ErrStatementRangeTooLarge; this test proves the repository
// layer's contract that makes that check possible).
func TestSchemaContract_Statement_RangeTooLarge_LimitPlusOne(t *testing.T) {
	db := setupSchemaTestDB(t)
	loc := jakartaLoc(t)
	entryRepo := repository.NewEntryRepository(db)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)

	const maxRows = 5
	day1 := time.Date(2026, 5, 10, 8, 0, 0, 0, loc)
	balance := int64(0)
	for i := 0; i < maxRows+1; i++ {
		balance += 100
		seedCreditEntry(t, db, cashA, 100, balance, day1.Add(time.Duration(i)*time.Minute))
	}

	entries, err := entryRepo.ListByAccountRange(ctx, cashA, day1, day1, loc, maxRows+1)
	require.NoError(t, err)
	require.Len(t, entries, maxRows+1, "LIMIT+1 must return maxRows+1 rows when that many exist, so the caller can detect the overflow")
}

// ─── docs/plan/16 Task T1: maker-checker adjustments ───────────────────────

// TestSchemaContract_PendingAdjustment_DBConstraint_RejectsSelfApprove
// bypasses Go entirely — a raw SQL UPDATE trying to set approved_by equal
// to requested_by — and proves the DB CHECK constraint on
// pending_adjustments rejects it. This is the backstop for
// apperror.ErrSelfApproval (the Go-level check in adjustments.Service),
// holding even if that check is somehow skipped or buggy.
func TestSchemaContract_PendingAdjustment_DBConstraint_RejectsSelfApprove(t *testing.T) {
	db := setupSchemaTestDB(t)
	ctx := context.Background()

	id := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO pending_adjustments (id, requested_by, cmd_payload, reason)
		VALUES ($1, 'user-A', '{}'::jsonb, 'test')`, id)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		UPDATE pending_adjustments SET approved_by = 'user-A', status = 'approved', decided_at = now()
		WHERE id = $1`, id)
	require.Error(t, err, "DB CHECK constraint must reject approved_by = requested_by even via raw SQL")
}

// TestSchemaContract_PendingAdjustment_ConcurrentApprove_ExactlyOneWins
// races N distinct approvers against the same pending adjustment — proves
// MarkApproved's atomic UPDATE (WHERE status='pending') lets exactly one
// win, same guarantee pattern as docs/plan/14 Task T2's CloseOriginal.
func TestSchemaContract_PendingAdjustment_ConcurrentApprove_ExactlyOneWins(t *testing.T) {
	db := setupSchemaTestDB(t)
	adjSvc, _ := newAdjustmentsService(db)
	ctx := context.Background()

	targetUser := uuid.New()
	createUserCashAccount(t, db, targetUser)
	id, err := adjSvc.Create(ctx, "requester", "adjustment_credit", decimal.NewFromInt(10_000), targetUser, nil, "concurrent-approve-test")
	require.NoError(t, err)

	const attempts = 8
	results := make([]error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = adjSvc.Approve(ctx, id, fmt.Sprintf("approver-%d", i))
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, err := range results {
		if err == nil {
			successCount++
		} else {
			require.True(t, errors.Is(err, apperror.ErrAdjustmentAlreadyDecided),
				"non-winning approve must fail with ErrAdjustmentAlreadyDecided, got: %v", err)
		}
	}
	require.Equal(t, 1, successCount, "exactly one of %d concurrent approvals must win the race", attempts)
}

// TestSchemaContract_PendingAdjustment_RetryApprove_NoDoublePost proves that
// calling Approve again after a successful approve never posts a second
// ledger transaction — the atomic status guard (no longer 'pending') stops
// the retry before it reaches Post, which is the concrete meaning of
// "idempotent" the docs/plan/16 Task T1 test list asks for.
func TestSchemaContract_PendingAdjustment_RetryApprove_NoDoublePost(t *testing.T) {
	db := setupSchemaTestDB(t)
	adjSvc, _ := newAdjustmentsService(db)
	ctx := context.Background()

	targetUser := uuid.New()
	createUserCashAccount(t, db, targetUser)
	id, err := adjSvc.Create(ctx, "requester", "adjustment_credit", decimal.NewFromInt(5_000), targetUser, nil, "retry-test")
	require.NoError(t, err)

	txID1, err := adjSvc.Approve(ctx, id, "approver-1")
	require.NoError(t, err)

	// Retry with the SAME approver (simulating a client retry after a lost
	// response) — must NOT post a second transaction.
	_, err = adjSvc.Approve(ctx, id, "approver-1")
	require.Error(t, err)
	require.True(t, errors.Is(err, apperror.ErrAdjustmentAlreadyDecided))

	var count int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM ledger_transactions WHERE idempotency_key = $1`, "adj:"+id.String(),
	).Scan(&count))
	require.Equal(t, 1, count, "must be exactly one posted transaction for this adjustment, not two")

	var storedTxID uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT executed_tx_id FROM pending_adjustments WHERE id = $1`, id,
	).Scan(&storedTxID))
	require.Equal(t, txID1, storedTxID)
}

// TestSchemaContract_PendingAdjustment_FullFlow_MovesBalance is the
// end-to-end proof: create -> approve -> the target account's balance
// actually moved, the system adjustment account moved the other way, and
// the ledger stays balanced.
func TestSchemaContract_PendingAdjustment_FullFlow_MovesBalance(t *testing.T) {
	db := setupSchemaTestDB(t)
	adjSvc, _ := newAdjustmentsService(db)
	ctx := context.Background()

	targetUser := uuid.New()
	cashID := createUserCashAccount(t, db, targetUser)

	id, err := adjSvc.Create(ctx, "ops-1", "adjustment_credit", decimal.NewFromInt(25_000), targetUser, nil, "compensation for outage")
	require.NoError(t, err)

	pending, err := adjSvc.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Status)
	require.Equal(t, "ops-1", pending.RequestedBy)

	txID, err := adjSvc.Approve(ctx, id, "ops-2")
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, txID)

	require.True(t, getBalance(t, db, cashID).Equal(decimal.NewFromInt(25_000)))

	decided, err := adjSvc.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "executed", decided.Status)
	require.NotNil(t, decided.ApprovedBy)
	require.Equal(t, "ops-2", *decided.ApprovedBy)
	require.NotNil(t, decided.ExecutedTxID)
	require.Equal(t, txID, *decided.ExecutedTxID)

	rows, err := db.QueryContext(ctx, `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity')`)
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next(), "fn_verify_ledger_balance found an unbalanced transaction after adjustment")
	require.NoError(t, rows.Err())

	// The audit event landed in the outbox with the right governance trail.
	var eventType string
	var rawPayload []byte
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT event_type, payload FROM outbox_events WHERE aggregate_id = $1`, id,
	).Scan(&eventType, &rawPayload))
	require.Equal(t, events.TypeAdjustmentDecided, eventType)

	var decidedEvent events.AdjustmentDecided
	require.NoError(t, json.Unmarshal(rawPayload, &decidedEvent))
	require.Equal(t, "ops-1", decidedEvent.RequestedBy)
	require.Equal(t, "ops-2", decidedEvent.ApprovedBy)
	require.Equal(t, "approved", decidedEvent.Decision)
	require.NotNil(t, decidedEvent.ExecutedTxID)
	require.Equal(t, txID, *decidedEvent.ExecutedTxID)
}

// TestSchemaContract_PendingAdjustment_Reject_NoMoneyMoves proves rejecting
// a pending adjustment never touches any balance.
func TestSchemaContract_PendingAdjustment_Reject_NoMoneyMoves(t *testing.T) {
	db := setupSchemaTestDB(t)
	adjSvc, _ := newAdjustmentsService(db)
	ctx := context.Background()

	targetUser := uuid.New()
	cashID := createUserCashAccount(t, db, targetUser)

	id, err := adjSvc.Create(ctx, "ops-1", "adjustment_credit", decimal.NewFromInt(9_999), targetUser, nil, "should be rejected")
	require.NoError(t, err)

	require.NoError(t, adjSvc.Reject(ctx, id, "ops-2"))

	require.True(t, getBalance(t, db, cashID).IsZero(), "rejected adjustment must never move money")

	rejected, err := adjSvc.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "rejected", rejected.Status)
	require.Nil(t, rejected.ExecutedTxID)
}

// ─── docs/plan/16 Task T2: external reconciliation ─────────────────────────

// TestSchemaContract_Recon_Matcher_AllFourStatuses posts three real internal
// transactions via the normal posting engine (so gateway/external_ref land
// on ledger_transactions exactly as production would write them), imports a
// CSV batch that intentionally matches one, mismatches one, and omits one,
// and adds one external_ref the ledger has never seen — then proves the
// set-based matcher (RunMatcher) classifies all four correctly in one pass.
func TestSchemaContract_Recon_Matcher_AllFourStatuses(t *testing.T) {
	db := setupSchemaTestDB(t)
	handleSvc, _ := newService(db)
	reconSvc, _, _ := newReconService(db)
	ctx := context.Background()
	reportDate := time.Now().UTC().Truncate(24 * time.Hour)

	userID := uuid.New()
	createUserCashAccount(t, db, userID)

	// Ledger has these three, all gateway=bca:
	//   ref-matched          10_000  -> CSV also says 10_000  => matched
	//   ref-mismatch         20_000  -> CSV says      25_000  => amount_mismatch
	//   ref-missing-external 30_000  -> CSV has no row at all => missing_external
	for _, tc := range []struct {
		key, ref string
		amount   int64
	}{
		{"recon-t1", "ref-matched", 10_000},
		{"recon-t2", "ref-mismatch", 20_000},
		{"recon-t3", "ref-missing-external", 30_000},
	} {
		err := handleSvc.Handle(ctx, processors.Command{
			IdempotencyKey: tc.key, Type: "money_in", Amount: decimal.NewFromInt(tc.amount),
			UserID: userID, Metadata: map[string]any{"gateway": "bca", "external_ref": tc.ref},
		})
		require.NoError(t, err)
	}

	// CSV has a row for a ref the ledger never posted => missing_internal.
	rows := []recon.ImportRow{
		{ExternalRef: "ref-matched", Amount: decimal.NewFromInt(10_000), SettledAt: reportDate.Format("2006-01-02")},
		{ExternalRef: "ref-mismatch", Amount: decimal.NewFromInt(25_000), SettledAt: reportDate.Format("2006-01-02")},
		{ExternalRef: "ref-orphan", Amount: decimal.NewFromInt(5_000), SettledAt: reportDate.Format("2006-01-02")},
	}

	batchID, err := reconSvc.ImportBatch(ctx, "bca", reportDate, "settlement-bca.csv", rows, "ops-1")
	require.NoError(t, err)

	report, err := reconSvc.GetBatchReport(ctx, batchID, "", 100, 0)
	require.NoError(t, err)
	require.Equal(t, "completed", report.Batch.Status)
	require.Equal(t, 3, report.Batch.RowCount)

	require.Equal(t, 1, report.Counts["matched"])
	require.Equal(t, 1, report.Counts["amount_mismatch"])
	require.Equal(t, 1, report.Counts["missing_internal"])
	require.Equal(t, 1, report.Counts["missing_external"])

	byRef := make(map[string]model.ReconItem, len(report.Items))
	for _, it := range report.Items {
		byRef[it.ExternalRef] = it
	}

	matched, ok := byRef["ref-matched"]
	require.True(t, ok)
	require.Equal(t, "matched", matched.MatchStatus)
	require.NotNil(t, matched.MatchedTxID)

	mismatch, ok := byRef["ref-mismatch"]
	require.True(t, ok)
	require.Equal(t, "amount_mismatch", mismatch.MatchStatus)
	require.NotNil(t, mismatch.MatchedTxID)
	require.True(t, mismatch.Amount.Equal(decimal.NewFromInt(25_000)), "recon_items.amount must be the REPORT's amount, not the ledger's")

	orphan, ok := byRef["ref-orphan"]
	require.True(t, ok)
	require.Equal(t, "missing_internal", orphan.MatchStatus)
	require.Nil(t, orphan.MatchedTxID)

	missingExternal, ok := byRef["ref-missing-external"]
	require.True(t, ok)
	require.Equal(t, "missing_external", missingExternal.MatchStatus)
	require.NotNil(t, missingExternal.MatchedTxID)
	require.True(t, missingExternal.Amount.Equal(decimal.NewFromInt(30_000)), "missing_external item's amount must come from the ledger transaction")
}

// TestSchemaContract_Recon_ResolveItem_CreatesAdjustment_ApproveMovesBalance
// is the end-to-end proof for K5 step 5 ("uang tidak bergerak tanpa approve
// manusia kedua"): resolving a non-matched item creates a pending
// adjustment but does NOT move money; only a second identity's Approve
// actually credits the gateway's suspense account.
func TestSchemaContract_Recon_ResolveItem_CreatesAdjustment_ApproveMovesBalance(t *testing.T) {
	db := setupSchemaTestDB(t)
	reconSvc, adjSvc, accRepo := newReconService(db)
	ctx := context.Background()
	reportDate := time.Now().UTC().Truncate(24 * time.Hour)

	suspenseBCA, err := accRepo.GetSystemAccountID(ctx, constant.AccountTypeSuspense, "suspense:bca", "IDR")
	require.NoError(t, err)
	require.True(t, getBalance(t, db, suspenseBCA).IsZero())

	rows := []recon.ImportRow{
		{ExternalRef: "ref-orphan", Amount: decimal.NewFromInt(7_000), SettledAt: reportDate.Format("2006-01-02")},
	}
	batchID, err := reconSvc.ImportBatch(ctx, "bca", reportDate, "settlement-bca.csv", rows, "ops-1")
	require.NoError(t, err)

	report, err := reconSvc.GetBatchReport(ctx, batchID, "missing_internal", 100, 0)
	require.NoError(t, err)
	require.Len(t, report.Items, 1)
	itemID := report.Items[0].ID

	// amount=0 means "use the recon item's own amount" (7_000).
	adjustmentID, err := reconSvc.ResolveItem(ctx, itemID, "ops-1", "adjustment_suspense_credit", decimal.Zero, "compensate for orphan settlement")
	require.NoError(t, err)

	// Create() alone must never move money.
	require.True(t, getBalance(t, db, suspenseBCA).IsZero(), "resolving must not move money before approval")

	pending, err := adjSvc.Get(ctx, adjustmentID)
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Status)
	require.Equal(t, "ops-1", pending.RequestedBy)
	require.Contains(t, pending.Reason, itemID.String(), "reason must reference the recon_item id for audit")

	txID, err := adjSvc.Approve(ctx, adjustmentID, "ops-2")
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, txID)

	require.True(t, getBalance(t, db, suspenseBCA).Equal(decimal.NewFromInt(7_000)), "approval must credit the suspense account")

	resolvedItem, err := reconSvc.GetBatchReport(ctx, batchID, "", 100, 0)
	require.NoError(t, err)
	require.Len(t, resolvedItem.Items, 1)
	require.NotNil(t, resolvedItem.Items[0].ResolvedByAdjustmentID)
	require.Equal(t, adjustmentID, *resolvedItem.Items[0].ResolvedByAdjustmentID)

	rowsCheck, err := db.QueryContext(ctx, `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity')`)
	require.NoError(t, err)
	defer rowsCheck.Close()
	require.False(t, rowsCheck.Next(), "fn_verify_ledger_balance found an unbalanced transaction after recon resolution")
	require.NoError(t, rowsCheck.Err())
}

// TestSchemaContract_Recon_DBConstraint_UniqueExternalRefPerBatch proves the
// UNIQUE(batch_id, external_ref) constraint holds even bypassing Go, so a
// caller can never have two conflicting rows for the same external_ref in
// one batch.
func TestSchemaContract_Recon_DBConstraint_UniqueExternalRefPerBatch(t *testing.T) {
	db := setupSchemaTestDB(t)
	ctx := context.Background()

	batchID := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO recon_batches (id, gateway, report_date, source_filename, row_count, status, created_by)
		VALUES ($1, 'bca', now()::date, 'f.csv', 1, 'completed', 'ops-1')`, batchID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO recon_items (id, batch_id, external_ref, amount, match_status)
		VALUES ($1, $2, 'dup-ref', 1000, 'missing_internal')`, uuid.New(), batchID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO recon_items (id, batch_id, external_ref, amount, match_status)
		VALUES ($1, $2, 'dup-ref', 2000, 'missing_internal')`, uuid.New(), batchID)
	require.Error(t, err, "UNIQUE(batch_id, external_ref) must reject a duplicate external_ref within the same batch")
}

// ─── docs/plan/16 Task T3: RLS & DB roles ───────────────────────────────────

// TestSchemaContract_AppServiceRole_FullFlowSucceeds is T3's most important
// test (per the plan's own "Test wajib"): every write the application
// actually performs — money_in, transfer_p2p, an adjustment create+approve,
// a recon import+resolve+approve — run through a connection that ONLY has
// migrations/000009_rls_roles.up.sql's app_service grants, proving those
// grants are sufficient for the app to function, not just that the GRANT
// statements themselves have valid syntax.
func TestSchemaContract_AppServiceRole_FullFlowSucceeds(t *testing.T) {
	dbs := setupAppServiceTestDB(t)
	ctx := context.Background()

	handleSvc, _ := newService(dbs.appDB)
	adjSvc := adjustments.New(dbs.appDB,
		repository.NewPendingAdjustmentRepository(dbs.appDB),
		repository.NewTransactionRepository(dbs.appDB),
		repository.NewOutboxRepository(dbs.appDB), handleSvc)
	reconSvc := recon.New(dbs.appDB, repository.NewReconRepository(dbs.appDB), adjSvc)

	userA := uuid.New()
	userB := uuid.New()
	cashA := createUserCashAccount(t, dbs.appDB, userA)
	cashB := createUserCashAccount(t, dbs.appDB, userB)

	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "app-svc-1", Type: "money_in", Amount: decimal.NewFromInt(50_000),
		UserID: userA, Metadata: map[string]any{"gateway": "bca", "external_ref": "app-svc-ref-1"},
	}))
	require.True(t, getBalance(t, dbs.appDB, cashA).Equal(decimal.NewFromInt(50_000)))

	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "app-svc-2", Type: "transfer_p2p", Amount: decimal.NewFromInt(10_000),
		UserID: userA, TargetUserID: userB,
	}))
	require.True(t, getBalance(t, dbs.appDB, cashB).Equal(decimal.NewFromInt(10_000)))

	adjID, err := adjSvc.Create(ctx, "ops-1", "adjustment_credit", decimal.NewFromInt(1_000), userB, nil, "app_service grant test")
	require.NoError(t, err)
	_, err = adjSvc.Approve(ctx, adjID, "ops-2")
	require.NoError(t, err)
	require.True(t, getBalance(t, dbs.appDB, cashB).Equal(decimal.NewFromInt(11_000)))

	reportDate := time.Now().UTC().Truncate(24 * time.Hour)
	batchID, err := reconSvc.ImportBatch(ctx, "gopay", reportDate, "f.csv",
		[]recon.ImportRow{{ExternalRef: "app-svc-recon-ref", Amount: decimal.NewFromInt(2_000)}}, "ops-1")
	require.NoError(t, err)
	report, err := reconSvc.GetBatchReport(ctx, batchID, "missing_internal", 10, 0)
	require.NoError(t, err)
	require.Len(t, report.Items, 1)

	reconAdjID, err := reconSvc.ResolveItem(ctx, report.Items[0].ID, "ops-1", "adjustment_suspense_credit", decimal.Zero, "app_service grant test")
	require.NoError(t, err)
	_, err = adjSvc.Approve(ctx, reconAdjID, "ops-2")
	require.NoError(t, err)

	rows, err := dbs.appDB.QueryContext(ctx, `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity')`)
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next(), "fn_verify_ledger_balance found an unbalanced transaction under app_service")
	require.NoError(t, rows.Err())
}

// TestSchemaContract_AppServiceRole_CannotUpdateLedgerEntries proves the
// app_service grant itself (not just the immutability trigger) rejects a
// direct UPDATE on ledger_entries — a second, independent layer under the
// trigger (docs/plan/16 Task T3 step 2).
func TestSchemaContract_AppServiceRole_CannotUpdateLedgerEntries(t *testing.T) {
	dbs := setupAppServiceTestDB(t)
	ctx := context.Background()

	_, err := dbs.appDB.ExecContext(ctx, `UPDATE ledger_entries SET note = 'hack' WHERE false`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "permission denied")
}

// TestSchemaContract_AppReadonlyRole_CannotWrite proves app_readonly has no
// write access anywhere, even to a table it CAN read.
func TestSchemaContract_AppReadonlyRole_CannotWrite(t *testing.T) {
	dbs := setupAppServiceTestDB(t)
	ctx := context.Background()

	_, err := dbs.appReadonlyDB.ExecContext(ctx, `
		INSERT INTO accounts (id, owner_type, type, currency, created_by)
		VALUES (gen_random_uuid(), 'system', 'adjustment', 'IDR', 'test')`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "permission denied")
}

// TestSchemaContract_AppReadonlyRole_CannotSeeOutboxOrAdjustments proves
// app_readonly's table-level exclusion of outbox_events and
// pending_adjustments (docs/plan/16 Task T3 step 3 — internal payloads, not
// for reporting consumption).
func TestSchemaContract_AppReadonlyRole_CannotSeeOutboxOrAdjustments(t *testing.T) {
	dbs := setupAppServiceTestDB(t)
	ctx := context.Background()

	_, err := dbs.appReadonlyDB.QueryContext(ctx, `SELECT count(*) FROM outbox_events`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "permission denied")

	_, err = dbs.appReadonlyDB.QueryContext(ctx, `SELECT count(*) FROM pending_adjustments`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "permission denied")
}

// TestSchemaContract_AppReadonlyRole_CanReadEverythingElse is the mirror —
// proves app_readonly's grant is not accidentally too narrow either.
func TestSchemaContract_AppReadonlyRole_CanReadEverythingElse(t *testing.T) {
	dbs := setupAppServiceTestDB(t)
	ctx := context.Background()

	for _, table := range []string{
		"accounts", "account_balances", "ledger_transactions", "ledger_entries",
		"recon_batches", "recon_items", "account_balance_snapshots", "v_account_balance_audit",
	} {
		var count int
		err := dbs.appReadonlyDB.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count)
		require.NoError(t, err, "app_readonly must be able to SELECT %s", table)
	}
}

// ─── docs/plan/17 Task T2: point-in-time rebuild ───────────────────────────

// rebuildProjectionSQL reads scripts/sql/rebuild_projection.sql — the exact
// same file scripts/rebuild-projection.sh executes — so this test proves
// the SQL the ops script actually runs, not a copy that could drift.
func rebuildProjectionSQL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "scripts", "sql", "rebuild_projection.sql")
	b, err := os.ReadFile(path)
	require.NoError(t, err, "read rebuild_projection.sql")
	return string(b)
}

// TestSchemaContract_RebuildProjection posts real transactions (money_in,
// transfer_p2p, and an adjustment via the T1 maker-checker flow so the
// suspense/adjustment system accounts are exercised too), corrupts
// account_balances directly via SQL to simulate a projection drift, runs
// the exact rebuild SQL the ops script uses, and proves every account's
// balance is restored from ledger_entries alone — AND that allow_negative
// (not derived from entries) survives untouched, the entire point of using
// UPDATE instead of TRUNCATE+replay (docs/plan/17 Task T2).
func TestSchemaContract_RebuildProjection(t *testing.T) {
	db := setupSchemaTestDB(t)
	handleSvc, accRepo := newService(db)
	adjSvc, _ := newAdjustmentsService(db)
	ctx := context.Background()

	userA := uuid.New()
	userB := uuid.New()
	cashA := createUserCashAccount(t, db, userA)
	cashB := createUserCashAccount(t, db, userB)

	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "rebuild-1", Type: "money_in", Amount: decimal.NewFromInt(100_000),
		UserID: userA, Metadata: map[string]any{"gateway": "bca"},
	}))
	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "rebuild-2", Type: "transfer_p2p", Amount: decimal.NewFromInt(30_000),
		UserID: userA, TargetUserID: userB,
	}))
	adjID, err := adjSvc.Create(ctx, "ops-1", "adjustment_credit", decimal.NewFromInt(5_000), userB, nil, "rebuild test")
	require.NoError(t, err)
	_, err = adjSvc.Approve(ctx, adjID, "ops-2")
	require.NoError(t, err)

	require.True(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(70_000)))
	require.True(t, getBalance(t, db, cashB).Equal(decimal.NewFromInt(35_000)))

	settlementBCA, err := accRepo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, "bca", "IDR")
	require.NoError(t, err)

	// allow_negative baseline BEFORE corruption/rebuild — must be identical
	// after, proving UPDATE (not TRUNCATE) preserved it.
	allowNegBefore := make(map[uuid.UUID]bool)
	for _, accID := range []uuid.UUID{cashA, cashB, settlementBCA} {
		var an bool
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT allow_negative FROM account_balances WHERE account_id = $1`, accID).Scan(&an))
		allowNegBefore[accID] = an
	}

	// Corrupt the projection directly — simulates drift a restore or a bug
	// could cause. Touch a user account AND the settlement system account.
	_, err = db.ExecContext(ctx, `UPDATE account_balances SET balance = balance + 999 WHERE account_id = $1`, cashA)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE account_balances SET balance = balance - 500 WHERE account_id = $1`, settlementBCA)
	require.NoError(t, err)

	// Prove the corruption actually landed (sanity on the test itself).
	require.False(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(70_000)))

	_, err = db.ExecContext(ctx, rebuildProjectionSQL(t))
	require.NoError(t, err, "rebuild SQL must execute cleanly")

	require.True(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(70_000)), "cashA must be restored from entries")
	require.True(t, getBalance(t, db, cashB).Equal(decimal.NewFromInt(35_000)), "cashB was never corrupted, must be untouched")

	for accID, wantAllowNeg := range allowNegBefore {
		var an bool
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT allow_negative FROM account_balances WHERE account_id = $1`, accID).Scan(&an))
		require.Equal(t, wantAllowNeg, an, "allow_negative for %s must survive rebuild unchanged", accID)
	}

	// Every account, not just the ones touched above, must now be
	// consistent — the same query the shell script uses to decide pass/fail.
	var inconsistent int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT count(*) FROM account_balances ab
		WHERE ab.balance IS DISTINCT FROM (
		    SELECT COALESCE(SUM(amount) FILTER (WHERE direction='credit'), 0) -
		           COALESCE(SUM(amount) FILTER (WHERE direction='debit'),  0)
		    FROM ledger_entries WHERE account_id = ab.account_id
		)`).Scan(&inconsistent))
	require.Equal(t, 0, inconsistent)

	rows, err := db.QueryContext(ctx, `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity')`)
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next())
	require.NoError(t, rows.Err())
}

// TestSchemaContract_RebuildProjection_IdempotentNoOp proves running the
// rebuild SQL against an already-consistent projection changes zero rows —
// safe to re-run, never destructive.
func TestSchemaContract_RebuildProjection_IdempotentNoOp(t *testing.T) {
	db := setupSchemaTestDB(t)
	handleSvc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)
	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "rebuild-noop-1", Type: "money_in", Amount: decimal.NewFromInt(42_000),
		UserID: userA, Metadata: map[string]any{"gateway": "bca"},
	}))

	res, err := db.ExecContext(ctx, rebuildProjectionSQL(t))
	require.NoError(t, err)
	rowsAffected, err := res.RowsAffected()
	require.NoError(t, err)
	require.Zero(t, rowsAffected, "rebuild against an already-consistent projection must change 0 rows")

	require.True(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(42_000)))
}

// ─── docs/plan/18 Task T1: currency registry ────────────────────────────────

// TestSchemaContract_CurrencyRepository_ListEnabled proves the repository
// reads migrations/000011's seed data correctly and respects enabled=false.
func TestSchemaContract_CurrencyRepository_ListEnabled(t *testing.T) {
	db := setupSchemaTestDB(t)
	repo := repository.NewCurrencyRepository(db)
	ctx := context.Background()

	list, err := repo.ListEnabled(ctx)
	require.NoError(t, err)

	byCode := make(map[string]int16, len(list))
	for _, c := range list {
		byCode[c.Code] = c.MinorUnit
	}
	require.Equal(t, int16(0), byCode["IDR"])
	require.Equal(t, int16(2), byCode["USD"])

	_, err = db.ExecContext(ctx, `UPDATE currencies SET enabled = false WHERE code = 'USD'`)
	require.NoError(t, err)

	list, err = repo.ListEnabled(ctx)
	require.NoError(t, err)
	byCode = make(map[string]int16, len(list))
	for _, c := range list {
		byCode[c.Code] = c.MinorUnit
	}
	_, hasUSD := byCode["USD"]
	require.False(t, hasUSD, "disabled currencies must not appear in ListEnabled")
	require.Contains(t, byCode, "IDR")
}

// TestSchemaContract_LoadCurrencies_PopulatesRuntimeRegistry is the
// end-to-end proof: reading migrations/000011's seed data via the
// repository and feeding it into pkg/currency.Load makes currency.IsValid
// reflect exactly what's in the DB.
func TestSchemaContract_LoadCurrencies_PopulatesRuntimeRegistry(t *testing.T) {
	db := setupSchemaTestDB(t)
	repo := repository.NewCurrencyRepository(db)
	ctx := context.Background()

	list, err := repo.ListEnabled(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, list)

	currency.Load(list)
	t.Cleanup(func() { currency.Load([]currency.Currency{{Code: "IDR", MinorUnit: 0}}) })

	require.True(t, currency.IsValid("IDR"))
	require.True(t, currency.IsValid("USD"))
	require.False(t, currency.IsValid("ZZZ"))

	exp, ok := currency.MinorUnit("USD")
	require.True(t, ok)
	require.Equal(t, int16(2), exp)
}

// TestSchemaContract_Provisioning_RespectsLoadedCurrencyRegistry is the
// end-to-end proof T1's own test list asks for: once the registry has been
// loaded from the `currencies` table (IDR+USD), provisioning a USD user
// succeeds; a currency outside the table is still rejected.
func TestSchemaContract_Provisioning_RespectsLoadedCurrencyRegistry(t *testing.T) {
	db := setupSchemaTestDB(t)
	repo := repository.NewCurrencyRepository(db)
	ctx := context.Background()

	list, err := repo.ListEnabled(ctx)
	require.NoError(t, err)
	currency.Load(list)
	t.Cleanup(func() { currency.Load([]currency.Currency{{Code: "IDR", MinorUnit: 0}}) })

	userID := uuid.New()
	accounts, err := provision.New(db).CreateUserAccounts(ctx, userID, "USD")
	require.NoError(t, err, "USD must be provisionable once the registry has loaded it from the DB")
	require.NotEmpty(t, accounts)
	for _, acc := range accounts {
		require.Equal(t, "USD", acc.Currency)
	}

	_, err = provision.New(db).CreateUserAccounts(ctx, uuid.New(), "XYZ")
	require.Error(t, err, "a currency absent from the currencies table must still be rejected")
	require.True(t, errors.Is(err, apperror.ErrValidation))
}

// loadFullCurrencyRegistry is docs/plan/18 Task T2's own test setup: load
// IDR+USD from the real `currencies` table (migration 000011) into the
// process-global registry, and reset it back to the IDR-only bootstrap
// default afterward so this test's state can't leak into another test in
// the same binary.
func loadFullCurrencyRegistry(t *testing.T, db *database.DBSQL) {
	t.Helper()
	list, err := repository.NewCurrencyRepository(db).ListEnabled(context.Background())
	require.NoError(t, err)
	currency.Load(list)
	t.Cleanup(func() { currency.Load([]currency.Currency{{Code: "IDR", MinorUnit: 0}}) })
}

// cashAccountOf finds a user's cash account id from provision.CreateUserAccounts' result.
func cashAccountOf(t *testing.T, accounts []model.Account) uuid.UUID {
	t.Helper()
	for _, acc := range accounts {
		if acc.Type == constant.AccountTypeCash {
			return acc.ID
		}
	}
	t.Fatalf("no cash account in provisioned set")
	return uuid.Nil
}

// TestSchemaContract_MultiCurrency_MoneyIn_UsesCorrectSettlementPool is
// docs/plan/18 Task T2's first required integration test: provision a USD
// user, money_in via settlement[bca], and prove the movement actually
// touched the USD settlement pool (not the IDR one seeded by 000002) — the
// exact bug T2 exists to prevent (currency-blind system account lookup
// silently mixing pools).
func TestSchemaContract_MultiCurrency_MoneyIn_UsesCorrectSettlementPool(t *testing.T) {
	db := setupSchemaTestDB(t)
	loadFullCurrencyRegistry(t, db)
	svc, accRepo := newService(db)
	ctx := context.Background()

	userUSD := uuid.New()
	accountsUSD, err := provision.New(db).CreateUserAccounts(ctx, userUSD, "USD")
	require.NoError(t, err)
	cashUSD := cashAccountOf(t, accountsUSD)

	err = svc.Handle(ctx, processors.Command{
		IdempotencyKey: "usd-topup-1",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(5_000),
		UserID:         userUSD,
		Metadata:       map[string]any{"gateway": "bca"},
	})
	require.NoError(t, err)
	require.True(t, getBalance(t, db, cashUSD).Equal(decimal.NewFromInt(5_000)))

	settlementUSD, err := accRepo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, "bca", "USD")
	require.NoError(t, err)
	settlementIDR, err := accRepo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, "bca", "IDR")
	require.NoError(t, err)
	require.NotEqual(t, settlementUSD, settlementIDR, "USD and IDR settlement[bca] must be distinct accounts")

	src, dest := getSourceDest(t, db, "usd-topup-1")
	require.Equal(t, settlementUSD, src, "money_in USD must debit the USD settlement pool, not IDR's")
	require.Equal(t, cashUSD, dest)

	// The IDR pool must be untouched by this USD posting.
	require.True(t, getBalance(t, db, settlementIDR).IsZero())
}

// TestSchemaContract_MultiCurrency_TransferP2P_CrossCurrencyRejected proves
// the existing currency-mismatch guard (service/handle/service.go
// validateAccounts) still fires correctly once accounts can genuinely hold
// different currencies — this is the invariant that makes "one ledger
// transaction is always one currency" true even with multiple pools live.
func TestSchemaContract_MultiCurrency_TransferP2P_CrossCurrencyRejected(t *testing.T) {
	db := setupSchemaTestDB(t)
	loadFullCurrencyRegistry(t, db)
	svc, _ := newService(db)
	ctx := context.Background()

	userIDR := uuid.New()
	accountsIDR, err := provision.New(db).CreateUserAccounts(ctx, userIDR, "IDR")
	require.NoError(t, err)
	cashIDR := cashAccountOf(t, accountsIDR)

	userUSD := uuid.New()
	_, err = provision.New(db).CreateUserAccounts(ctx, userUSD, "USD")
	require.NoError(t, err)

	err = svc.Handle(ctx, processors.Command{
		IdempotencyKey: "topup-for-mismatch-test",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(100_000),
		UserID:         userIDR,
		Metadata:       map[string]any{"gateway": "bca"},
	})
	require.NoError(t, err)
	require.True(t, getBalance(t, db, cashIDR).Equal(decimal.NewFromInt(100_000)))

	err = svc.Handle(ctx, processors.Command{
		IdempotencyKey: "cross-currency-transfer",
		Type:           "transfer_p2p",
		Amount:         decimal.NewFromInt(1_000),
		UserID:         userIDR,
		TargetUserID:   userUSD,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, apperror.ErrCurrencyMismatch), "IDR->USD transfer_p2p must be rejected as a currency mismatch, got: %v", err)
	require.Equal(t, 0, countLedgerTransactions(t, db, "cross-currency-transfer"), "a rejected currency-mismatch transfer must not post any transaction")
}

// TestSchemaContract_MultiCurrency_ParallelIDRAndUSD_HitDistinctSettlementAccounts
// is docs/plan/18 Task T2's second required integration test: post IDR and
// USD money_in through the SAME gateway ("bca") and prove each lands on its
// own settlement account, not a shared/arbitrary one.
func TestSchemaContract_MultiCurrency_ParallelIDRAndUSD_HitDistinctSettlementAccounts(t *testing.T) {
	db := setupSchemaTestDB(t)
	loadFullCurrencyRegistry(t, db)
	svc, accRepo := newService(db)
	ctx := context.Background()

	userIDR := uuid.New()
	accountsIDR, err := provision.New(db).CreateUserAccounts(ctx, userIDR, "IDR")
	require.NoError(t, err)
	cashIDR := cashAccountOf(t, accountsIDR)

	userUSD := uuid.New()
	accountsUSD, err := provision.New(db).CreateUserAccounts(ctx, userUSD, "USD")
	require.NoError(t, err)
	cashUSD := cashAccountOf(t, accountsUSD)

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "parallel-idr",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(200_000),
		UserID:         userIDR,
		Metadata:       map[string]any{"gateway": "bca"},
	}))
	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "parallel-usd",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(2_000),
		UserID:         userUSD,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	require.True(t, getBalance(t, db, cashIDR).Equal(decimal.NewFromInt(200_000)))
	require.True(t, getBalance(t, db, cashUSD).Equal(decimal.NewFromInt(2_000)))

	settlementIDR, err := accRepo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, "bca", "IDR")
	require.NoError(t, err)
	settlementUSD, err := accRepo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, "bca", "USD")
	require.NoError(t, err)

	srcIDR, _ := getSourceDest(t, db, "parallel-idr")
	srcUSD, _ := getSourceDest(t, db, "parallel-usd")
	require.Equal(t, settlementIDR, srcIDR)
	require.Equal(t, settlementUSD, srcUSD)
	require.NotEqual(t, srcIDR, srcUSD)

	require.True(t, getBalance(t, db, settlementIDR).Equal(decimal.NewFromInt(-200_000)))
	require.True(t, getBalance(t, db, settlementUSD).Equal(decimal.NewFromInt(-2_000)))
}

// getTransactionID looks up a posted transaction's id by idempotency key —
// used by the FX tests below to build a reversal Command's ReferenceID.
func getTransactionID(t *testing.T, db *database.DBSQL, idempotencyKey string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := db.QueryRowContext(context.Background(),
		`SELECT id FROM ledger_transactions WHERE idempotency_key = $1`, idempotencyKey).Scan(&id)
	require.NoError(t, err)
	return id
}

// fxCommand builds the shared metadata contract both fx_out and fx_in
// require (docs/plan/18 Task T3) — quote_id/rate/pair, none of which are
// ever used arithmetically by the processors themselves.
func fxCommand(idemKey, txType string, amount decimal.Decimal, userID uuid.UUID, quoteID string) processors.Command {
	return processors.Command{
		IdempotencyKey: idemKey,
		Type:           txType,
		Amount:         amount,
		UserID:         userID,
		Metadata:       map[string]any{"quote_id": quoteID, "rate": "15800", "pair": "IDRUSD"},
	}
}

// TestSchemaContract_FX_OutThenIn_MovesBothLegsCorrectly is docs/plan/18
// Task T3's first required integration test. fx_out and fx_in are two
// independent single-currency ledger transactions (K-S S2: FX is
// orchestration, not a ledger feature) — the ledger has no notion of "the
// same end user" holding two currencies at once (GetAccountID resolves
// exactly one active cash account per user), so this test represents the
// two legs as two separate identities, which is how a real orchestrator
// would model a per-currency omnibus/settlement account anyway.
func TestSchemaContract_FX_OutThenIn_MovesBothLegsCorrectly(t *testing.T) {
	db := setupSchemaTestDB(t)
	loadFullCurrencyRegistry(t, db)
	svc, accRepo := newService(db)
	ctx := context.Background()

	userIDR := uuid.New()
	accountsIDR, err := provision.New(db).CreateUserAccounts(ctx, userIDR, "IDR")
	require.NoError(t, err)
	cashIDR := cashAccountOf(t, accountsIDR)

	userUSD := uuid.New()
	accountsUSD, err := provision.New(db).CreateUserAccounts(ctx, userUSD, "USD")
	require.NoError(t, err)
	cashUSD := cashAccountOf(t, accountsUSD)

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "fx-fund-idr",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(1_000_000),
		UserID:         userIDR,
		Metadata:       map[string]any{"gateway": "bca"},
	}))
	require.True(t, getBalance(t, db, cashIDR).Equal(decimal.NewFromInt(1_000_000)))

	fxIDR, err := accRepo.GetSystemAccountID(ctx, constant.AccountTypeFxConversion, "IDRUSD", "IDR")
	require.NoError(t, err)
	fxUSD, err := accRepo.GetSystemAccountID(ctx, constant.AccountTypeFxConversion, "IDRUSD", "USD")
	require.NoError(t, err)

	require.NoError(t, svc.Handle(ctx, fxCommand("fx:q1:out", "fx_out", decimal.NewFromInt(100_000), userIDR, "q1")))
	require.True(t, getBalance(t, db, cashIDR).Equal(decimal.NewFromInt(900_000)), "fx_out must debit the user's IDR cash")
	require.True(t, getBalance(t, db, fxIDR).Equal(decimal.NewFromInt(100_000)), "fx_out must credit the IDR fx_conversion leg")

	require.NoError(t, svc.Handle(ctx, fxCommand("fx:q1:in", "fx_in", decimal.NewFromInt(6), userUSD, "q1")))
	require.True(t, getBalance(t, db, cashUSD).Equal(decimal.NewFromInt(6)), "fx_in must credit the user's USD cash")
	require.True(t, getBalance(t, db, fxUSD).Equal(decimal.NewFromInt(-6)), "fx_in must debit the USD fx_conversion leg (allow_negative)")

	rows, err := db.QueryContext(ctx, `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity')`)
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next(), "fn_verify_ledger_balance found an unbalanced transaction")
	require.NoError(t, rows.Err())
}

// TestSchemaContract_FX_In_RetrySameKey_Idempotent is docs/plan/18 Task T3's
// second required integration test.
func TestSchemaContract_FX_In_RetrySameKey_Idempotent(t *testing.T) {
	db := setupSchemaTestDB(t)
	loadFullCurrencyRegistry(t, db)
	svc, _ := newService(db)
	ctx := context.Background()

	userUSD := uuid.New()
	accountsUSD, err := provision.New(db).CreateUserAccounts(ctx, userUSD, "USD")
	require.NoError(t, err)
	cashUSD := cashAccountOf(t, accountsUSD)

	cmd := fxCommand("fx:q2:in", "fx_in", decimal.NewFromInt(50), userUSD, "q2")
	require.NoError(t, svc.Handle(ctx, cmd))
	require.NoError(t, svc.Handle(ctx, cmd), "retrying the exact same idempotency key must succeed, not error")

	require.Equal(t, 1, countLedgerTransactions(t, db, "fx:q2:in"), "retry must not create a second transaction")
	require.True(t, getBalance(t, db, cashUSD).Equal(decimal.NewFromInt(50)), "balance must reflect exactly one posting, not two")
}

// TestSchemaContract_FX_InFails_OpenPositionVisible_ReversalCloses is
// docs/plan/18 Task T3's third required integration test — proves the
// runbook's (docs/runbooks/fx-position.md) core claim: a failed second leg
// leaves a VISIBLE non-zero fx_conversion balance, and reversing the posted
// leg brings it back to zero.
func TestSchemaContract_FX_InFails_OpenPositionVisible_ReversalCloses(t *testing.T) {
	db := setupSchemaTestDB(t)
	loadFullCurrencyRegistry(t, db)
	svc, accRepo := newService(db)
	ctx := context.Background()

	userIDR := uuid.New()
	accountsIDR, err := provision.New(db).CreateUserAccounts(ctx, userIDR, "IDR")
	require.NoError(t, err)
	cashIDR := cashAccountOf(t, accountsIDR)

	userUSD := uuid.New()
	accountsUSD, err := provision.New(db).CreateUserAccounts(ctx, userUSD, "USD")
	require.NoError(t, err)
	cashUSDID := cashAccountOf(t, accountsUSD)

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "fx-fund-idr-2",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(500_000),
		UserID:         userIDR,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	fxIDR, err := accRepo.GetSystemAccountID(ctx, constant.AccountTypeFxConversion, "IDRUSD", "IDR")
	require.NoError(t, err)

	// Leg 1 (fx_out) succeeds.
	require.NoError(t, svc.Handle(ctx, fxCommand("fx:q3:out", "fx_out", decimal.NewFromInt(200_000), userIDR, "q3")))
	require.True(t, getBalance(t, db, fxIDR).Equal(decimal.NewFromInt(200_000)), "open position must be visible after leg 1")

	// Suspend the destination account so leg 2 (fx_in) fails permanently.
	// GetAccountID's own lookup filters status='active' (repository/
	// account_repository.go), so a suspended destination surfaces as
	// ErrAccountNotFound at ResolveAccounts time — the account simply isn't
	// visible to the lookup — rather than ErrAccountSuspended from the
	// deeper structural check in validateAccounts (which only ever sees
	// accounts a repository lookup already returned). Either way the
	// outcome the runbook cares about is identical: the leg fails, nothing
	// posts, the position stays open.
	_, err = db.ExecContext(ctx, `UPDATE accounts SET status = 'suspended' WHERE id = $1`, cashUSDID)
	require.NoError(t, err)

	err = svc.Handle(ctx, fxCommand("fx:q3:in", "fx_in", decimal.NewFromInt(12), userUSD, "q3"))
	require.Error(t, err, "fx_in must fail against a suspended destination account")
	require.True(t, errors.Is(err, apperror.ErrAccountNotFound))
	require.Equal(t, 0, countLedgerTransactions(t, db, "fx:q3:in"), "a failed fx_in must not post any transaction")

	// The position is still open — exactly the runbook's premise.
	require.True(t, getBalance(t, db, fxIDR).Equal(decimal.NewFromInt(200_000)), "position must remain open after the failed leg")

	// Ops decision: reverse the posted leg (fx_out) instead of retrying.
	fxOutTxID := getTransactionID(t, db, "fx:q3:out")
	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "fx:q3:out:reversal",
		Type:           "reversal",
		Amount:         decimal.NewFromInt(200_000),
		ReferenceID:    fxOutTxID,
	}))

	require.True(t, getBalance(t, db, fxIDR).IsZero(), "reversal must close the open position back to zero")
	require.True(t, getBalance(t, db, cashIDR).Equal(decimal.NewFromInt(500_000)), "reversal must return the user's IDR cash to its pre-fx_out balance")
}

// dateOnly truncates a time.Time to midnight in its own location — used so
// scheduled-transaction integration tests compare calendar dates the same
// way the DATE columns in scheduled_transactions do.
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// scheduleIdempotencyKeyForTest mirrors service/schedule's unexported
// scheduleIdempotencyKey — duplicated here deliberately (same pattern as
// this file's other hardcoded idempotency key literals, e.g. "fx:q1:out")
// rather than exporting a test-only helper from the production package.
func scheduleIdempotencyKeyForTest(id uuid.UUID, asOf time.Time) string {
	return "sched:" + id.String() + ":" + asOf.Format("2006-01-02")
}

// TestSchemaContract_Schedule_DailyRunDue_IdempotentAcrossDays is
// docs/plan/19 Task T1's first required integration test: create daily ->
// RunDue(day1) posts; RunDue(day1) again is idempotent (no double post);
// RunDue(day2) posts a second time.
func TestSchemaContract_Schedule_DailyRunDue_IdempotentAcrossDays(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newScheduleService(db)
	ctx := context.Background()

	userA := uuid.New()
	userB := uuid.New()
	cashA := createUserCashAccount(t, db, userA)
	cashB := createUserCashAccount(t, db, userB)

	handleSvc, _ := newService(db)
	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "sched-test-fund",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(100_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	day1 := dateOnly(time.Now())
	id, err := svc.Create(ctx, userA, "transfer_p2p", decimal.NewFromInt(1_000), userB, "", nil, "daily", day1, nil, userA.String())
	require.NoError(t, err)

	executed, failed, err := svc.RunDue(ctx, day1)
	require.NoError(t, err)
	assert.Equal(t, 1, executed)
	assert.Equal(t, 0, failed)
	require.True(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(99_000)))
	require.True(t, getBalance(t, db, cashB).Equal(decimal.NewFromInt(1_000)))
	require.Equal(t, 1, countLedgerTransactions(t, db, scheduleIdempotencyKeyForTest(id, day1)))

	// Same day again — must not double-post.
	executed, failed, err = svc.RunDue(ctx, day1)
	require.NoError(t, err)
	assert.Equal(t, 0, executed, "already ran today — must not be due again")
	assert.Equal(t, 0, failed)
	require.True(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(99_000)), "balance must not move a second time")
	require.Equal(t, 1, countLedgerTransactions(t, db, scheduleIdempotencyKeyForTest(id, day1)))

	// Next day — due again, second real posting.
	day2 := day1.AddDate(0, 0, 1)
	executed, failed, err = svc.RunDue(ctx, day2)
	require.NoError(t, err)
	assert.Equal(t, 1, executed)
	assert.Equal(t, 0, failed)
	require.True(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(98_000)))
	require.True(t, getBalance(t, db, cashB).Equal(decimal.NewFromInt(2_000)))
	require.Equal(t, 1, countLedgerTransactions(t, db, scheduleIdempotencyKeyForTest(id, day2)))
}

// TestSchemaContract_Schedule_CrashWindow_AlreadyPostedTreatedAsSuccess
// is docs/plan/19 Task T1's crash-window integration test: simulate a prior
// run that posted successfully but crashed before last_run_date was
// updated (by posting the exact same idempotency key manually), then prove
// RunDue treats the resulting ErrAlreadyPosted as success and still writes
// last_run_date.
func TestSchemaContract_Schedule_CrashWindow_AlreadyPostedTreatedAsSuccess(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, scheduleRepo := newScheduleService(db)
	ctx := context.Background()

	userA := uuid.New()
	userB := uuid.New()
	createUserCashAccount(t, db, userA)
	createUserCashAccount(t, db, userB)

	handleSvc, _ := newService(db)
	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "sched-crash-fund",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(50_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	asOf := dateOnly(time.Now())
	id, err := svc.Create(ctx, userA, "transfer_p2p", decimal.NewFromInt(500), userB, "", nil, "once", asOf, nil, userA.String())
	require.NoError(t, err)

	// Simulate the crash: post directly with the SAME key RunDue would use,
	// bypassing the service entirely — the row's last_run_date stays NULL.
	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey:   scheduleIdempotencyKeyForTest(id, asOf),
		IdempotencyScope: userA.String(),
		Type:             "transfer_p2p",
		Amount:           decimal.NewFromInt(500),
		UserID:           userA,
		TargetUserID:     userB,
	}))
	before, err := scheduleRepo.GetByID(ctx, id)
	require.NoError(t, err)
	require.Nil(t, before.LastRunDate, "crash simulated: last_run_date must still be unset")

	executed, failed, err := svc.RunDue(ctx, asOf)
	require.NoError(t, err)
	assert.Equal(t, 1, executed, "ErrAlreadyPosted must be treated as success")
	assert.Equal(t, 0, failed)
	require.Equal(t, 1, countLedgerTransactions(t, db, scheduleIdempotencyKeyForTest(id, asOf)), "must still be exactly one transaction, not two")

	after, err := scheduleRepo.GetByID(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, after.LastRunDate, "RunDue must write last_run_date even when Post returned ErrAlreadyPosted")
	require.Equal(t, "finished", after.Status, "'once' schedule must finish after the crash-window retry")
}

// TestSchemaContract_Schedule_ListDue_PerScheduleKind is docs/plan/19 Task
// T1's table-driven due-selection test, run directly against the
// repository (the SQL logic itself — the layer that actually implements
// per-kind matching — rather than mocked).
func TestSchemaContract_Schedule_ListDue_PerScheduleKind(t *testing.T) {
	db := setupSchemaTestDB(t)
	_, scheduleRepo := newScheduleService(db)
	ctx := context.Background()

	asOf := dateOnly(time.Now())
	otherDayOfMonth := (asOf.Day() % 28) + 1
	if otherDayOfMonth == asOf.Day() {
		otherDayOfMonth = (otherDayOfMonth % 28) + 1
	}

	type testRow struct {
		name        string
		kind        string
		runAtDate   time.Time
		dayOfMonth  *int
		lastRunDate *time.Time
		status      string
		wantDue     bool
	}
	monthDay := asOf.Day()
	rows := []testRow{
		{"once_today_never_run", "once", asOf, nil, nil, "active", true},
		{"once_past_never_run_catches_up", "once", asOf.AddDate(0, 0, -3), nil, nil, "active", true},
		{"once_future_not_due", "once", asOf.AddDate(0, 0, 1), nil, nil, "active", false},
		{"daily_not_run_today", "daily", asOf.AddDate(0, 0, -10), nil, ptrTime(asOf.AddDate(0, 0, -1)), "active", true},
		{"daily_already_run_today", "daily", asOf.AddDate(0, 0, -10), nil, ptrTime(asOf), "active", false},
		{"monthly_matching_day", "monthly", asOf.AddDate(0, -2, 0), &monthDay, nil, "active", true},
		{"monthly_nonmatching_day", "monthly", asOf.AddDate(0, -2, 0), &otherDayOfMonth, nil, "active", false},
		{"paused_otherwise_due", "daily", asOf.AddDate(0, 0, -10), nil, nil, "paused", false},
	}

	ids := make(map[string]uuid.UUID, len(rows))
	for _, r := range rows {
		id := uuid.New()
		ids[r.name] = id
		payload := []byte(`{"type":"transfer_p2p","amount":"100"}`)
		require.NoError(t, db.WithTx(ctx, nil, func(tx *sql.Tx) error {
			if err := scheduleRepo.Create(ctx, tx, id, uuid.New(), payload, r.kind, r.runAtDate, r.dayOfMonth, "test"); err != nil {
				return err
			}
			if r.lastRunDate != nil {
				if err := scheduleRepo.MarkSuccess(ctx, tx, id, *r.lastRunDate, false); err != nil {
					return err
				}
			}
			if r.status == "paused" {
				if _, err := scheduleRepo.Pause(ctx, tx, id); err != nil {
					return err
				}
			}
			return nil
		}))
	}

	due, err := scheduleRepo.ListDue(ctx, asOf)
	require.NoError(t, err)
	dueIDs := make(map[uuid.UUID]bool, len(due))
	for _, d := range due {
		dueIDs[d.ID] = true
	}

	for _, r := range rows {
		got := dueIDs[ids[r.name]]
		assert.Equal(t, r.wantDue, got, "case %q: wantDue=%v gotDue=%v", r.name, r.wantDue, got)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// TestSchemaContract_Disbursement_ImportThenRun_AllPostedAcrossMultipleCalls
// is docs/plan/19 Task T2's first required integration test: import 10
// items -> run twice with maxItemsPerRun=6 -> all 10 posted, balances
// correct, exactly one transaction per item key.
func TestSchemaContract_Disbursement_ImportThenRun_AllPostedAcrossMultipleCalls(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newDisbursementService(db, 6)
	ctx := context.Background()

	users := make([]uuid.UUID, 10)
	cash := make([]uuid.UUID, 10)
	rows := make([]model.DisbursementImportRow, 10)
	for i := 0; i < 10; i++ {
		users[i] = uuid.New()
		cash[i] = createUserCashAccount(t, db, users[i])
		rows[i] = model.DisbursementImportRow{UserID: users[i], Amount: decimal.NewFromInt(int64(100 * (i + 1)))}
	}

	batchID, err := svc.Import(ctx, "payroll.csv", rows, "ops")
	require.NoError(t, err)

	result1, err := svc.Run(ctx, batchID, false)
	require.NoError(t, err)
	assert.Equal(t, 6, result1.Processed)
	assert.Equal(t, 6, result1.Posted)
	assert.False(t, result1.Done, "6 of 10 processed — batch must not be done yet")

	result2, err := svc.Run(ctx, batchID, false)
	require.NoError(t, err)
	assert.Equal(t, 4, result2.Processed)
	assert.Equal(t, 4, result2.Posted)
	assert.True(t, result2.Done)

	for i := 0; i < 10; i++ {
		require.True(t, getBalance(t, db, cash[i]).Equal(decimal.NewFromInt(int64(100*(i+1)))),
			"item %d balance mismatch", i+1)
		require.Equal(t, 1, countLedgerTransactions(t, db, "batch:"+batchID.String()+":"+fmtInt(i+1)))
	}

	var status string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM disbursement_batches WHERE id = $1`, batchID).Scan(&status))
	require.Equal(t, "completed", status)
}

func fmtInt(n int) string { return fmt.Sprintf("%d", n) }

// TestSchemaContract_Disbursement_Resume_NoDoublePost is docs/plan/19 Task
// T2's resume integration test: simulate a "process died mid-batch" state
// by posting item 5 directly (bypassing Run) before ever calling Run, then
// prove Run only advances items 1-4 and 6-10 (5 stays untouched by Run,
// already posted) with no double-posting.
func TestSchemaContract_Disbursement_Resume_NoDoublePost(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, disbRepo := newDisbursementService(db, 100)
	handleSvc, _ := newService(db)
	ctx := context.Background()

	users := make([]uuid.UUID, 10)
	cash := make([]uuid.UUID, 10)
	rows := make([]model.DisbursementImportRow, 10)
	for i := 0; i < 10; i++ {
		users[i] = uuid.New()
		cash[i] = createUserCashAccount(t, db, users[i])
		rows[i] = model.DisbursementImportRow{UserID: users[i], Amount: decimal.NewFromInt(500)}
	}

	batchID, err := svc.Import(ctx, "payroll2.csv", rows, "ops")
	require.NoError(t, err)

	// Simulate item 5 having already posted in a prior (crashed) run —
	// post directly with the deterministic key Run would use, then mark it
	// posted in the repository, exactly what Run itself would have done.
	item5Key := "batch:" + batchID.String() + ":5"
	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: item5Key,
		Type:           "disbursement",
		Amount:         decimal.NewFromInt(500),
		UserID:         users[4],
	}))
	items, err := disbRepo.ListItems(ctx, batchID, "", 100, 0)
	require.NoError(t, err)
	var item5ID uuid.UUID
	for _, it := range items {
		if it.ItemNo == 5 {
			item5ID = it.ID
		}
	}
	require.NotEqual(t, uuid.Nil, item5ID)
	postedTx, err := repository.NewTransactionRepository(db).GetByIdempotencyKey(ctx, item5Key, nil)
	require.NoError(t, err)
	require.NoError(t, db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return disbRepo.MarkItemPosted(ctx, tx, item5ID, postedTx.ID)
	}))

	// Resume: Run again — must post items 1-4 and 6-10 (9 items), leave 5 untouched.
	result, err := svc.Run(ctx, batchID, false)
	require.NoError(t, err)
	assert.Equal(t, 9, result.Processed, "item 5 must not be re-selected — it's already 'posted'")
	assert.True(t, result.Done)

	for i := 0; i < 10; i++ {
		require.True(t, getBalance(t, db, cash[i]).Equal(decimal.NewFromInt(500)), "item %d balance mismatch", i+1)
		require.Equal(t, 1, countLedgerTransactions(t, db, "batch:"+batchID.String()+":"+fmtInt(i+1)),
			"item %d must have exactly one transaction, never two", i+1)
	}
}

// TestSchemaContract_Disbursement_BusinessFailure_OtherItemsStillProcess is
// docs/plan/19 Task T2's business-failure integration test: one item
// targets a user with no cash account (business failure at ResolveAccounts
// time) — it must be marked 'failed' with an error, the OTHER items must
// still post, and the batch ends 'completed_with_errors'.
func TestSchemaContract_Disbursement_BusinessFailure_OtherItemsStillProcess(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, disbRepo := newDisbursementService(db, 100)
	ctx := context.Background()

	goodUser1 := uuid.New()
	goodCash1 := createUserCashAccount(t, db, goodUser1)
	badUser := uuid.New() // never provisioned — no cash account
	goodUser2 := uuid.New()
	goodCash2 := createUserCashAccount(t, db, goodUser2)

	rows := []model.DisbursementImportRow{
		{UserID: goodUser1, Amount: decimal.NewFromInt(1_000)},
		{UserID: badUser, Amount: decimal.NewFromInt(2_000)},
		{UserID: goodUser2, Amount: decimal.NewFromInt(3_000)},
	}
	batchID, err := svc.Import(ctx, "payroll3.csv", rows, "ops")
	require.NoError(t, err)

	result, err := svc.Run(ctx, batchID, false)
	require.NoError(t, err)
	assert.Equal(t, 3, result.Processed)
	assert.Equal(t, 2, result.Posted)
	assert.Equal(t, 1, result.Failed)
	assert.True(t, result.Done)

	require.True(t, getBalance(t, db, goodCash1).Equal(decimal.NewFromInt(1_000)))
	require.True(t, getBalance(t, db, goodCash2).Equal(decimal.NewFromInt(3_000)))

	items, err := disbRepo.ListItems(ctx, batchID, "failed", 10, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, badUser, items[0].UserID)
	require.NotNil(t, items[0].Error)
	require.NotEmpty(t, *items[0].Error)

	var status string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM disbursement_batches WHERE id = $1`, batchID).Scan(&status))
	require.Equal(t, "completed_with_errors", status)
}

// TestSchemaContract_Accrual_BasicFlow_IdempotentAcrossRuns is docs/plan/19
// Task T3's basic integration test: fund an account -> snapshot -> accrue
// -> balance increases correctly with a unique key; running the job again
// for the same date is idempotent; the ledger verifier stays clean.
func TestSchemaContract_Accrual_BasicFlow_IdempotentAcrossRuns(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, savingsRepo := newAccrualService(db)
	handleSvc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)

	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "accrual-fund-1",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(1_000_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	require.NoError(t, db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return savingsRepo.Upsert(ctx, tx, model.SavingsConfig{AccountID: cashA, AnnualRateBps: 500, Enabled: true})
	}))

	today := dateOnly(time.Now())
	snapshotRepo := repository.NewSnapshotRepository(db, time.UTC)
	_, err := snapshotRepo.InsertForDate(ctx, today)
	require.NoError(t, err)

	wantInterest := accrual.DailyInterest(decimal.NewFromInt(1_000_000), 500)
	require.True(t, wantInterest.IsPositive(), "test setup must produce a non-zero interest amount")

	accrued, skipped := svc.RunDue(ctx, today)
	assert.Equal(t, 1, accrued)
	assert.Equal(t, 0, skipped)
	require.True(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(1_000_000).Add(wantInterest)))
	require.Equal(t, 1, countLedgerTransactions(t, db, "accrue:"+cashA.String()+":"+today.Format("2006-01-02")))

	// Same date again — must not double-post.
	accrued, skipped = svc.RunDue(ctx, today)
	assert.Equal(t, 1, accrued, "ErrAlreadyPosted must still count as accrued (idempotent)")
	assert.Equal(t, 0, skipped)
	require.True(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(1_000_000).Add(wantInterest)), "balance must not move a second time")
	require.Equal(t, 1, countLedgerTransactions(t, db, "accrue:"+cashA.String()+":"+today.Format("2006-01-02")))

	rows, err := db.QueryContext(ctx, `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity')`)
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next(), "fn_verify_ledger_balance found an unbalanced transaction")
	require.NoError(t, rows.Err())
}

// TestSchemaContract_Accrual_BasisIsSnapshotNotLiveBalance is docs/plan/19
// Task T3's DoD-required explicit proof: changing the LIVE balance after
// the snapshot was taken must NOT change the accrued amount — the basis is
// always the snapshot, never a live re-read.
func TestSchemaContract_Accrual_BasisIsSnapshotNotLiveBalance(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, savingsRepo := newAccrualService(db)
	handleSvc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)

	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "accrual-fund-2",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(1_000_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	require.NoError(t, db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return savingsRepo.Upsert(ctx, tx, model.SavingsConfig{AccountID: cashA, AnnualRateBps: 500, Enabled: true})
	}))

	today := dateOnly(time.Now())
	snapshotRepo := repository.NewSnapshotRepository(db, time.UTC)
	_, err := snapshotRepo.InsertForDate(ctx, today)
	require.NoError(t, err)

	// Change the LIVE balance AFTER the snapshot was taken — a much larger
	// deposit that, if accrual used a live re-read, would produce a very
	// different (much larger) interest amount.
	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "accrual-fund-2-after-snapshot",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(50_000_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))
	require.True(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(51_000_000)), "live balance must reflect the second deposit")

	snapshotBasedInterest := accrual.DailyInterest(decimal.NewFromInt(1_000_000), 500)
	liveBasedInterest := accrual.DailyInterest(decimal.NewFromInt(51_000_000), 500)
	require.NotEqual(t, snapshotBasedInterest, liveBasedInterest, "test must set up amounts large enough to actually differ")

	accrued, skipped := svc.RunDue(ctx, today)
	assert.Equal(t, 1, accrued)
	assert.Equal(t, 0, skipped)

	got := getBalance(t, db, cashA)
	require.True(t, got.Equal(decimal.NewFromInt(51_000_000).Add(snapshotBasedInterest)),
		"accrual must have used the SNAPSHOT balance (1,000,000), not the live balance (51,000,000) — got balance %s", got)
}

// TestSchemaContract_Accrual_DisabledAccount_NotAccrued is docs/plan/19
// Task T3's third required integration test: an account with
// enabled=false in savings_config must never be accrued.
func TestSchemaContract_Accrual_DisabledAccount_NotAccrued(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, savingsRepo := newAccrualService(db)
	handleSvc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	cashA := createUserCashAccount(t, db, userA)

	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "accrual-fund-3",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(1_000_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	require.NoError(t, db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return savingsRepo.Upsert(ctx, tx, model.SavingsConfig{AccountID: cashA, AnnualRateBps: 500, Enabled: false})
	}))

	today := dateOnly(time.Now())
	snapshotRepo := repository.NewSnapshotRepository(db, time.UTC)
	_, err := snapshotRepo.InsertForDate(ctx, today)
	require.NoError(t, err)

	accrued, skipped := svc.RunDue(ctx, today)
	assert.Equal(t, 0, accrued, "a disabled account must never be accrued")
	assert.Equal(t, 0, skipped, "a disabled account isn't even considered — ListEnabled excludes it entirely")
	require.True(t, getBalance(t, db, cashA).Equal(decimal.NewFromInt(1_000_000)), "balance must be untouched")
	require.Equal(t, 0, countLedgerTransactions(t, db, "accrue:"+cashA.String()+":"+today.Format("2006-01-02")))
}

// =============================================================================
// docs/plan/37 Task T3 — fraud screening removed from the posting pipeline
// =============================================================================

// TestSchemaContract_ExecTransfer_PostsWithoutAnyFraudClientConfigured
// proves the posting pipeline no longer has ANY screening seam
// (docs/plan/37 Task T3): newService wires no fraud client of any kind, yet
// a normal transfer_p2p posts successfully — there is nothing left in
// internal/ledger/service/handle for a caller to even configure.
// Screening now happens entirely in the transport layer, before Handle is
// ever called (see internal/ledger/transport's own block/fail-open tests).
func TestSchemaContract_ExecTransfer_PostsWithoutAnyFraudClientConfigured(t *testing.T) {
	db := setupSchemaTestDB(t)
	svc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	userB := uuid.New()
	_ = createUserCashAccount(t, db, userA)
	_ = createUserCashAccount(t, db, userB)

	require.NoError(t, svc.Handle(ctx, processors.Command{
		IdempotencyKey: "no-fraud-client-topup",
		Type:           "money_in",
		Amount:         decimal.NewFromInt(1_000_000),
		UserID:         userA,
		Metadata:       map[string]any{"gateway": "bca"},
	}))

	err := svc.Handle(ctx, processors.Command{
		IdempotencyKey: "no-fraud-client-transfer",
		Type:           "transfer_p2p",
		Amount:         decimal.NewFromInt(100_000),
		UserID:         userA,
		TargetUserID:   userB,
	})
	require.NoError(t, err)
}

// =============================================================================
// docs/plan/20 Task T2 — Regulatory reporting views
// =============================================================================

// seedPostedTransaction directly inserts a 'posted' ledger_transactions row
// at a controlled created_at — used by the timezone regression test, which
// needs a transaction backdated to a specific UTC instant that the normal
// posting engine (always "now") can't produce. Unlike seedCreditEntry, this
// writes ONLY the transaction header — v_report_daily_mutation reads
// ledger_transactions alone, no ledger_entries/account_balances needed.
func seedPostedTransaction(t *testing.T, db *database.DBSQL, txType string, amount int64, currency string, createdAt time.Time) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO ledger_transactions (id, idempotency_key, type, status, amount, currency, created_at, updated_at)
		VALUES ($1, $2, $3, 'posted', $4, $5, $6, $6)`,
		uuid.New(), "seed-mut-"+uuid.New().String(), txType, amount, currency, createdAt)
	require.NoError(t, err)
}

// TestSchemaContract_Reporting_DailyPositionMatchesManualAggregate proves
// v_report_daily_position's aggregate matches a manual sum over the
// underlying snapshot the accrual/statement jobs also read.
func TestSchemaContract_Reporting_DailyPositionMatchesManualAggregate(t *testing.T) {
	db := setupSchemaTestDB(t)
	handleSvc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	userB := uuid.New()
	cashA := createUserCashAccount(t, db, userA)
	cashB := createUserCashAccount(t, db, userB)

	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "report-pos-fund-a", Type: "money_in", Amount: decimal.NewFromInt(700_000),
		UserID: userA, Metadata: map[string]any{"gateway": "bca"},
	}))
	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "report-pos-fund-b", Type: "money_in", Amount: decimal.NewFromInt(300_000),
		UserID: userB, Metadata: map[string]any{"gateway": "bca"},
	}))
	_ = cashA
	_ = cashB

	today := dateOnly(time.Now())
	snapshotRepo := repository.NewSnapshotRepository(db, time.UTC)
	_, err := snapshotRepo.InsertForDate(ctx, today)
	require.NoError(t, err)

	var manualTotal int64
	var manualCount int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT count(*), sum(s.closing_balance) FROM account_balance_snapshots s
		JOIN accounts a ON a.id = s.account_id
		WHERE s.as_of_date = $1 AND a.currency = 'IDR' AND a.type = 'cash' AND a.owner_type = 'user'`,
		today.Format("2006-01-02")).Scan(&manualCount, &manualTotal))

	reportingRepo := repository.NewReportingRepository(db)
	rows, err := reportingRepo.DailyPosition(ctx, today, today)
	require.NoError(t, err)

	found := false
	for _, row := range rows {
		if row.Currency == "IDR" && row.AccountType == "cash" && row.OwnerType == "user" {
			found = true
			assert.Equal(t, manualCount, row.AccountCount)
			assert.True(t, row.TotalBalance.Equal(decimal.NewFromInt(manualTotal)),
				"view total %s must match manual aggregate %d", row.TotalBalance, manualTotal)
			assert.True(t, row.TotalBalance.Equal(decimal.NewFromInt(1_000_000)))
		}
	}
	require.True(t, found, "expected an IDR/cash/user row in v_report_daily_position")
}

// TestSchemaContract_Reporting_DailyMutationMatchesManualAggregate proves
// v_report_daily_mutation's per-type/currency aggregate matches a manual
// count+sum over ledger_transactions for the same WIB day.
func TestSchemaContract_Reporting_DailyMutationMatchesManualAggregate(t *testing.T) {
	db := setupSchemaTestDB(t)
	handleSvc, _ := newService(db)
	ctx := context.Background()

	userA := uuid.New()
	createUserCashAccount(t, db, userA)

	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "report-mut-1", Type: "money_in", Amount: decimal.NewFromInt(100_000),
		UserID: userA, Metadata: map[string]any{"gateway": "bca"},
	}))
	require.NoError(t, handleSvc.Handle(ctx, processors.Command{
		IdempotencyKey: "report-mut-2", Type: "money_in", Amount: decimal.NewFromInt(50_000),
		UserID: userA, Metadata: map[string]any{"gateway": "bca"},
	}))

	loc, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)
	today := dateOnly(time.Now().In(loc))

	var manualCount int
	var manualSum int64
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT count(*), sum(amount) FROM ledger_transactions
		WHERE status = 'posted' AND type = 'money_in'
		  AND (created_at AT TIME ZONE 'Asia/Jakarta')::date = $1`,
		today.Format("2006-01-02")).Scan(&manualCount, &manualSum))
	require.GreaterOrEqual(t, manualCount, 2)

	reportingRepo := repository.NewReportingRepository(db)
	rows, err := reportingRepo.DailyMutation(ctx, today, today)
	require.NoError(t, err)

	found := false
	for _, row := range rows {
		if row.TxType == "money_in" && row.Currency == "IDR" {
			found = true
			assert.Equal(t, manualCount, row.TxCount)
			assert.True(t, row.TotalAmount.Equal(decimal.NewFromInt(manualSum)))
		}
	}
	require.True(t, found, "expected a money_in/IDR row in v_report_daily_mutation")
}

// TestSchemaContract_Reporting_ReconSummaryMatchesManualAggregate proves
// v_report_recon_summary's per-batch match-status counts match a manual
// per-status count over recon_items (reusing 16-T2's exact scenario: one
// matched, one amount_mismatch, one missing_internal, one missing_external).
func TestSchemaContract_Reporting_ReconSummaryMatchesManualAggregate(t *testing.T) {
	db := setupSchemaTestDB(t)
	handleSvc, _ := newService(db)
	reconSvc, _, _ := newReconService(db)
	ctx := context.Background()

	userID := uuid.New()
	createUserCashAccount(t, db, userID)
	reportDate := dateOnly(time.Now())

	for _, tc := range []struct {
		key, ref string
		amount   int64
	}{
		{"report-recon-t1", "rref-matched", 10_000},
		{"report-recon-t2", "rref-mismatch", 20_000},
		{"report-recon-t3", "rref-missing-external", 30_000},
	} {
		require.NoError(t, handleSvc.Handle(ctx, processors.Command{
			IdempotencyKey: tc.key, Type: "money_in", Amount: decimal.NewFromInt(tc.amount),
			UserID: userID, Metadata: map[string]any{"gateway": "bca", "external_ref": tc.ref},
		}))
	}

	rows := []recon.ImportRow{
		{ExternalRef: "rref-matched", Amount: decimal.NewFromInt(10_000), SettledAt: reportDate.Format("2006-01-02")},
		{ExternalRef: "rref-mismatch", Amount: decimal.NewFromInt(25_000), SettledAt: reportDate.Format("2006-01-02")},
		{ExternalRef: "rref-orphan", Amount: decimal.NewFromInt(5_000), SettledAt: reportDate.Format("2006-01-02")},
	}
	batchID, err := reconSvc.ImportBatch(ctx, "bca", reportDate, "report-settlement-bca.csv", rows, "ops-1")
	require.NoError(t, err)

	var manualMatched, manualMismatch, manualMissingInternal, manualMissingExternal, manualItemCount int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE match_status='matched'),
		       count(*) FILTER (WHERE match_status='amount_mismatch'),
		       count(*) FILTER (WHERE match_status='missing_internal'),
		       count(*) FILTER (WHERE match_status='missing_external'),
		       count(*)
		FROM recon_items WHERE batch_id = $1`, batchID,
	).Scan(&manualMatched, &manualMismatch, &manualMissingInternal, &manualMissingExternal, &manualItemCount))

	reportingRepo := repository.NewReportingRepository(db)
	summaries, err := reportingRepo.ReconSummary(ctx, reportDate, reportDate)
	require.NoError(t, err)

	found := false
	for _, s := range summaries {
		if s.BatchID == batchID {
			found = true
			assert.Equal(t, "bca", s.Gateway)
			assert.Equal(t, manualItemCount, s.ItemCount)
			assert.Equal(t, manualMatched, s.MatchedCount)
			assert.Equal(t, manualMismatch, s.AmountMismatchCount)
			assert.Equal(t, manualMissingInternal, s.MissingInternalCount)
			assert.Equal(t, manualMissingExternal, s.MissingExternalCount)
			assert.Equal(t, 0, s.ResolvedCount, "nothing resolved yet in this scenario")
		}
	}
	require.True(t, found, "expected batch %s in v_report_recon_summary", batchID)
}

// TestSchemaContract_Reporting_AppReadonlyCanSelectViews proves an external
// BI tool connecting as app_readonly (docs/plan/16 Task T3's role split)
// can SELECT all three report views directly — this is the whole point of
// the views being the query contract (docs/plan/20 Task T2 step 1).
func TestSchemaContract_Reporting_AppReadonlyCanSelectViews(t *testing.T) {
	dbs := setupAppServiceTestDB(t)
	ctx := context.Background()

	for _, view := range []string{"v_report_daily_position", "v_report_daily_mutation", "v_report_recon_summary"} {
		var count int
		err := dbs.appReadonlyDB.QueryRowContext(ctx, "SELECT count(*) FROM "+view).Scan(&count)
		require.NoError(t, err, "app_readonly must be able to SELECT %s", view)
	}
}

// TestSchemaContract_Reporting_AppReadonlyBlockedFromPayloadTables proves
// app_readonly still cannot read outbox_events/pending_adjustments directly
// (docs/plan/20 Task T2's own DoD: the new views must not become a
// side-door around the docs/plan/16 Task T3 grant boundary — this
// re-asserts the boundary itself is unchanged by this task).
//
// scheduled_transactions is deliberately NOT in this list — migrations/
// 000014_scheduled_transactions.up.sql (docs/plan/19 Task T1, predating this
// task) already GRANTs app_readonly SELECT on it, unlike outbox_events/
// pending_adjustments. This task doesn't touch that grant either way; this
// test documents the actual current boundary rather than an assumed one.
func TestSchemaContract_Reporting_AppReadonlyBlockedFromPayloadTables(t *testing.T) {
	dbs := setupAppServiceTestDB(t)
	ctx := context.Background()

	for _, table := range []string{"outbox_events", "pending_adjustments"} {
		_, err := dbs.appReadonlyDB.QueryContext(ctx, "SELECT count(*) FROM "+table)
		require.Error(t, err, "app_readonly must NOT be able to SELECT %s", table)
		require.Contains(t, err.Error(), "permission denied")
	}
}

// TestSchemaContract_Reporting_TimezoneRegressionGuard is docs/plan/20 Task
// T2's mandatory regression guard for the docs/plan/16 Task T2 ::date vs
// ::timestamptz::date lesson: a transaction posted at 00:30 WIB (17:30 UTC
// the PREVIOUS day) must land on the correct WIB calendar date in
// v_report_daily_mutation, not the UTC date.
func TestSchemaContract_Reporting_TimezoneRegressionGuard(t *testing.T) {
	db := setupSchemaTestDB(t)
	ctx := context.Background()

	// 2026-03-10 00:30 WIB == 2026-03-09 17:30 UTC.
	wibDate := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	utcInstant := time.Date(2026, 3, 9, 17, 30, 0, 0, time.UTC)

	seedPostedTransaction(t, db, "money_in", 42_000, "IDR", utcInstant)

	reportingRepo := repository.NewReportingRepository(db)
	rows, err := reportingRepo.DailyMutation(ctx, wibDate, wibDate)
	require.NoError(t, err)

	found := false
	for _, row := range rows {
		if row.TxType == "money_in" && row.Currency == "IDR" {
			found = true
			assert.Equal(t, wibDate.Format("2006-01-02"), row.ReportDate.Format("2006-01-02"),
				"a transaction at 17:30 UTC (00:30 WIB the next day) must be attributed to the WIB calendar date")
			assert.True(t, row.TotalAmount.Equal(decimal.NewFromInt(42_000)))
		}
	}
	require.True(t, found, "expected the backdated transaction to appear on the WIB report_date, not the UTC one")

	// Negative check: it must NOT appear on the UTC calendar date instead.
	utcDate := dateOnly(utcInstant)
	if utcDate.Format("2006-01-02") != wibDate.Format("2006-01-02") {
		wrongDayRows, err := reportingRepo.DailyMutation(ctx, utcDate, utcDate)
		require.NoError(t, err)
		for _, row := range wrongDayRows {
			assert.NotEqual(t, "money_in", row.TxType, "transaction must not be misattributed to the UTC calendar date")
		}
	}
}
