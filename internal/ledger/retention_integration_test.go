//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T1's ledger retention
// classes end to end against a real Postgres (same throwaway-container
// pattern as schema_contract_test.go): the SECURITY DEFINER functions
// themselves (eligibility boundary, K8 proof-awareness, hold exclusion,
// direct-DELETE-still-forbidden) and pkg/retentionworker.Runner wired
// against them for real.
package ledger_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/retentionworker"
)

// setupLedgerOnlyDB is setupSchemaTestDB's single-service equivalent:
// setupSchemaTestDB deliberately applies EVERY service's migrations into
// one shared test database (testutil.ApplyServiceMigrations, version-
// tracked per service but physically merged — see that function's own
// comment: "the remaining monolith-era migrations"). retention_holds and
// retention_audit are intentionally named identically in all eight owner
// databases (docs/roadmap/archive/51 K5 — one shape, one production database each),
// which collides in that shared single-database harness the moment a
// second service's copy tries to CREATE TABLE. Real per-service-database
// deployment (production and every other live verification in this task)
// never sees this collision — it is purely an artifact of the merged test
// harness, so these tests apply only ledger's own migrations instead of
// reaching for setupSchemaTestDB.
func setupLedgerOnlyDB(t *testing.T) *database.DBSQL {
	t.Helper()
	db, _ := setupLedgerOnlyDBWithConfig(t)
	return db
}

// setupLedgerOnlyDBWithConfig is setupLedgerOnlyDB plus the owner
// connection's own config.PostgresConfig, for tests that need a second
// connection under a different role (e.g. TestRetention_..._DirectDeleteStillForbidden).
func setupLedgerOnlyDBWithConfig(t *testing.T) (*database.DBSQL, config.PostgresConfig) {
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

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		dbUser, dbPassword, host, port.Port(), dbName)
	require.NoError(t, testutil.ApplyMigration(migrationsSourceURL(t), "ledger", dsn))

	cfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 20,
	}
	db, err := database.New(ctx, cfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, cfg
}

func insertFeeQuote(t *testing.T, db *database.DBSQL, id, userID uuid.UUID, expiresAt time.Time, consumedAt *time.Time, consumedByRef string, feeAmount int64) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO fee_quotes (id, user_id, transaction_type, gateway, currency, amount, fee_amount, fee_gateway, expires_at, consumed_at, consumed_by_ref, created_at)
		VALUES ($1, $2, 'transfer_p2p', '', 'IDR', 100000, $3, 'platform', $4, $5, NULLIF($6, ''), $4)`,
		id, userID, feeAmount, expiresAt, consumedAt, consumedByRef)
	require.NoError(t, err)
}

func countFeeQuotes(t *testing.T, db *database.DBSQL) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT count(*) FROM fee_quotes`).Scan(&n))
	return n
}

func TestRetention_FeeQuotesUnconsumed_EligibilityBoundary(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()
	userID := uuid.New()

	notYetEligible := uuid.New() // expired 23h59m ago — inside the 24h grace window
	eligible := uuid.New()       // expired 24h00m01s ago — just past the boundary

	insertFeeQuote(t, db, notYetEligible, userID, time.Now().Add(-23*time.Hour-59*time.Minute), nil, "", 500)
	insertFeeQuote(t, db, eligible, userID, time.Now().Add(-24*time.Hour-1*time.Second), nil, "", 500)

	var dryRun int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_fee_quotes_unconsumed($1, 500, true)`, uuid.New()).Scan(&dryRun))
	require.Equal(t, 1, dryRun, "exactly the boundary-crossing quote should be eligible")

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_fee_quotes_unconsumed($1, 500, false)`, uuid.New()).Scan(&affected))
	require.Equal(t, 1, affected)
	require.Equal(t, 1, countFeeQuotes(t, db), "the not-yet-eligible quote must survive")
}

func TestRetention_FeeQuotesUnconsumed_DryRunMatchesReal(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()
	userID := uuid.New()
	for i := 0; i < 5; i++ {
		insertFeeQuote(t, db, uuid.New(), userID, time.Now().Add(-48*time.Hour), nil, "", 500)
	}

	var dryRun int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_fee_quotes_unconsumed($1, 500, true)`, uuid.New()).Scan(&dryRun))

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_fee_quotes_unconsumed($1, 500, false)`, uuid.New()).Scan(&affected))

	require.Equal(t, dryRun, affected)
	require.Equal(t, 5, affected)
}

func TestRetention_FeeQuotesUnconsumed_RetentionHoldExcludesRow(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()
	heldUser := uuid.New()
	quoteID := uuid.New()
	insertFeeQuote(t, db, quoteID, heldUser, time.Now().Add(-48*time.Hour), nil, "", 500)

	_, err := db.ExecContext(ctx, `
		INSERT INTO ledger_retention_holds (id, scope, scope_value, reason_code, created_by)
		VALUES ($1, 'subject', $2, 'legal_hold', 'tester')`, uuid.New(), heldUser.String())
	require.NoError(t, err)

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_fee_quotes_unconsumed($1, 500, true)`, uuid.New()).Scan(&affected))
	require.Equal(t, 0, affected, "an active subject-scoped hold must exclude the row")
	require.Equal(t, 1, countFeeQuotes(t, db))
}

func TestRetention_FeeQuotesUnconsumed_DirectDeleteStillForbidden(t *testing.T) {
	ownerDB, ownerCfg := setupLedgerOnlyDBWithConfig(t)
	ctx := context.Background()

	const appPassword = "app-test-pw"
	_, err := ownerDB.ExecContext(ctx, `CREATE ROLE test_retention_app_service LOGIN PASSWORD '`+appPassword+`'`)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, `GRANT app_service TO test_retention_app_service`)
	require.NoError(t, err)

	appCfg := ownerCfg
	appCfg.User, appCfg.Password = "test_retention_app_service", appPassword
	appDB, err := database.New(ctx, appCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })

	_, err = appDB.ExecContext(ctx, `DELETE FROM fee_quotes`)
	require.Error(t, err, "app_service must not be able to DELETE fee_quotes directly, only via the retention function")
}

func TestRetention_FeeQuotesConsumed_ProofAware(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()

	// 'fee' accounts are seeded by migrations/ledger/000002 (system
	// accounts); 'cash' accounts are only ever created per-user by the
	// provisioning service, never by a migration, so this test creates
	// one explicitly.
	var feeAcct uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM accounts WHERE type = 'fee' LIMIT 1`).Scan(&feeAcct))
	cashAcct := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO accounts (id, owner_id, owner_type, type, currency, created_by)
		VALUES ($1, $2, 'user', 'cash', 'IDR', 'test')`, cashAcct, uuid.New())
	require.NoError(t, err)

	// Matching proof: a posted transaction with a real fee-account entry
	// whose amount equals the quote's fee_amount.
	matchingTx := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_transactions (id, idempotency_key, type, status, amount, currency, source_account_id, destination_account_id, created_at, updated_at)
		VALUES ($1, $2, 'transfer_p2p', 'posted', 500, 'IDR', $3, $4, now() - interval '400 days', now() - interval '400 days')`,
		matchingTx, "match-"+matchingTx.String(), cashAcct, feeAcct)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transaction_id, account_id, direction, amount, balance_after, created_at)
		VALUES ($1, $2, $3, 'credit', 500, 500, now() - interval '400 days')`, uuid.New(), matchingTx, feeAcct)
	require.NoError(t, err)

	consumedAt := time.Now().Add(-400 * 24 * time.Hour)
	matchingQuote := uuid.New()
	insertFeeQuote(t, db, matchingQuote, uuid.New(), time.Now().Add(-401*24*time.Hour), &consumedAt, "tx:"+matchingTx.String(), 500)

	// Mismatched proof: references a posted tx, but with NO fee-account
	// entry at all (fee_amount says 999, booked fee is 0) — must never be
	// purged on a proof gap.
	mismatchTx := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_transactions (id, idempotency_key, type, status, amount, currency, source_account_id, destination_account_id, created_at, updated_at)
		VALUES ($1, $2, 'transfer_p2p', 'posted', 500, 'IDR', $3, $4, now() - interval '400 days', now() - interval '400 days')`,
		mismatchTx, "mismatch-"+mismatchTx.String(), cashAcct, feeAcct)
	require.NoError(t, err)
	mismatchQuote := uuid.New()
	insertFeeQuote(t, db, mismatchQuote, uuid.New(), time.Now().Add(-401*24*time.Hour), &consumedAt, "tx:"+mismatchTx.String(), 999)

	// Non-terminal proof: references a transaction that never posted.
	pendingTx := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_transactions (id, idempotency_key, type, status, amount, currency, source_account_id, destination_account_id, created_at, updated_at)
		VALUES ($1, $2, 'transfer_p2p', 'pending', 500, 'IDR', $3, $4, now() - interval '400 days', now() - interval '400 days')`,
		pendingTx, "pending-"+pendingTx.String(), cashAcct, feeAcct)
	require.NoError(t, err)
	pendingQuote := uuid.New()
	insertFeeQuote(t, db, pendingQuote, uuid.New(), time.Now().Add(-401*24*time.Hour), &consumedAt, "tx:"+pendingTx.String(), 500)

	// Payout-consumed: not verifiable from this database (cross-service) —
	// must never be purged by this function (documented limitation).
	payoutQuote := uuid.New()
	insertFeeQuote(t, db, payoutQuote, uuid.New(), time.Now().Add(-401*24*time.Hour), &consumedAt, "payout:"+uuid.New().String(), 500)

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_fee_quotes_consumed($1, 500, true)`, uuid.New()).Scan(&affected))
	require.Equal(t, 1, affected, "only the proof-matching, terminal, tx-referencing quote should be eligible")

	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_fee_quotes_consumed($1, 500, false)`, uuid.New()).Scan(&affected))
	require.Equal(t, 1, affected)

	remaining := map[uuid.UUID]bool{}
	rows, err := db.QueryContext(ctx, `SELECT id FROM fee_quotes`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		require.NoError(t, rows.Scan(&id))
		remaining[id] = true
	}
	require.False(t, remaining[matchingQuote], "the proof-matching quote must be gone")
	require.True(t, remaining[mismatchQuote], "a proof mismatch must never be purged")
	require.True(t, remaining[pendingQuote], "a non-terminal reference must never be purged")
	require.True(t, remaining[payoutQuote], "a payout: ref is unverifiable here and must never be purged")
}

// TestRetention_Runner_EndToEnd proves pkg/retentionworker.Runner
// wired against the real functions above: dry-run then real-run through
// the shared Go abstraction (not raw SQL), and that retention_audit rows
// land as K4 requires.
func TestRetention_Runner_EndToEnd(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()
	userID := uuid.New()
	for i := 0; i < 3; i++ {
		insertFeeQuote(t, db, uuid.New(), userID, time.Now().Add(-48*time.Hour), nil, "", 500)
	}

	runner, err := retentionworker.NewRunner("ledger", db, []retentionworker.Class{
		{Name: "ledger.fee_quotes.unconsumed", Action: "delete", FunctionName: "fn_retention_purge_fee_quotes_unconsumed"},
		{Name: "ledger.fee_quotes.consumed", Action: "delete", FunctionName: "fn_retention_purge_fee_quotes_consumed"},
		{Name: "ledger.outbox_events.published", Action: "delete", FunctionName: "fn_retention_purge_outbox_events_published"},
	})
	require.NoError(t, err)

	dryReport := runner.RunOnce(ctx, true)
	require.NoError(t, dryReport.Classes["ledger.fee_quotes.unconsumed"].Err)
	require.Equal(t, 3, dryReport.Classes["ledger.fee_quotes.unconsumed"].Affected)

	realReport := runner.RunOnce(ctx, false)
	require.NoError(t, realReport.Classes["ledger.fee_quotes.unconsumed"].Err)
	require.Equal(t, 3, realReport.Classes["ledger.fee_quotes.unconsumed"].Affected)
	require.Equal(t, 0, countFeeQuotes(t, db))

	var auditCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM ledger_retention_audit WHERE class = 'ledger.fee_quotes.unconsumed'`).Scan(&auditCount))
	require.Equal(t, 2, auditCount, "one audit run for the dry run, one for the real run")
}

// TestRetention_ConcurrentWorkers_NoDoubleProcessing is T1's own required
// test: "500-row batching, equal timestamps, concurrent workers, and
// restart." Two Runners share nothing (their own *retentionworker.Runner,
// same underlying *database.DBSQL, same table) and call RunOnce
// concurrently — FOR UPDATE SKIP LOCKED (every retention function's own
// eligible-CTE, e.g. fn_retention_purge_fee_quotes_unconsumed) is what
// this test actually exercises: it must be architecturally impossible for
// both workers to claim the same row, so the sum of what they each report
// affected must equal exactly the seeded count, with no row double-counted
// and none skipped. Equal timestamps are the adversarial case for this:
// all seeded rows share one expires_at, so ORDER BY expires_at alone
// cannot disambiguate between them — only the row lock can.
func TestRetention_ConcurrentWorkers_NoDoubleProcessing(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()
	userID := uuid.New()
	const seeded = 40
	sameExpiry := time.Now().Add(-48 * time.Hour)
	for i := 0; i < seeded; i++ {
		insertFeeQuote(t, db, uuid.New(), userID, sameExpiry, nil, "", 500)
	}

	runnerA, err := retentionworker.NewRunner("ledger", db, []retentionworker.Class{
		{Name: "ledger.fee_quotes.unconsumed", Action: "delete", FunctionName: "fn_retention_purge_fee_quotes_unconsumed"},
	}, retentionworker.WithBatchSize(5))
	require.NoError(t, err)
	runnerB, err := retentionworker.NewRunner("ledger", db, []retentionworker.Class{
		{Name: "ledger.fee_quotes.unconsumed", Action: "delete", FunctionName: "fn_retention_purge_fee_quotes_unconsumed"},
	}, retentionworker.WithBatchSize(5))
	require.NoError(t, err)

	var wg sync.WaitGroup
	var reportA, reportB retentionworker.Report
	wg.Add(2)
	go func() { defer wg.Done(); reportA = runnerA.RunOnce(ctx, false) }()
	go func() { defer wg.Done(); reportB = runnerB.RunOnce(ctx, false) }()
	wg.Wait()

	affectedA := reportA.Classes["ledger.fee_quotes.unconsumed"].Affected
	affectedB := reportB.Classes["ledger.fee_quotes.unconsumed"].Affected
	require.NoError(t, reportA.Classes["ledger.fee_quotes.unconsumed"].Err)
	require.NoError(t, reportB.Classes["ledger.fee_quotes.unconsumed"].Err)
	require.Equal(t, seeded, affectedA+affectedB, "every row must be claimed by exactly one worker — no double-processing, no row left behind")
	require.Equal(t, 0, countFeeQuotes(t, db))
}
