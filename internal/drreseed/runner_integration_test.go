//go:build integration

package drreseed_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/drreseed"
	"github.com/herdifirdausss/seev/internal/fraud"
	"github.com/herdifirdausss/seev/internal/policy"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/cache"
)

// setupLedgerDB provisions one testcontainers Postgres, creates
// seev_ledger, and applies its real migrations — miniredis (a real,
// in-process Redis-protocol server) stands in for the Redis target,
// avoiding a second container while still exercising the real
// go-redis/v9 client path drreseed itself uses.
func setupLedgerDB(t *testing.T) (dsn string, rdb *redis.Client) {
	t.Helper()
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:16.14-alpine",
		postgres.WithDatabase("postgres"), postgres.WithUsername("seev"), postgres.WithPassword("seev"),
		postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	baseDSN, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	admin, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE seev_ledger"); err != nil {
		t.Fatalf("create seev_ledger: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}
	dsn = fmt.Sprintf("postgres://seev:seev@%s:%s/seev_ledger?sslmode=disable", host, port.Port())
	if err := testutil.ApplyMigration("file://../../migrations", "ledger", dsn); err != nil {
		t.Fatalf("apply ledger migrations: %v", err)
	}

	mr := miniredis.RunT(t)
	rdb = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return dsn, rdb
}

func seedPostedTransfer(t *testing.T, db *sql.DB, userID uuid.UUID, amount int64) uuid.UUID {
	t.Helper()
	txID := uuid.New()
	otherOwner := uuid.New()
	var userCash, otherCash uuid.UUID
	if err := db.QueryRow(`INSERT INTO accounts (id, owner_id, owner_type, type, currency, status) VALUES (gen_random_uuid(), $1, 'user', 'cash', 'USD', 'active') RETURNING id`, userID).Scan(&userCash); err != nil {
		t.Fatalf("insert user account: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO account_balances (account_id, balance) VALUES ($1, 100000)`, userCash); err != nil {
		t.Fatalf("insert user balance: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO accounts (id, owner_id, owner_type, type, currency, status) VALUES (gen_random_uuid(), $1, 'user', 'cash', 'USD', 'active') RETURNING id`, otherOwner).Scan(&otherCash); err != nil {
		t.Fatalf("insert other account: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO account_balances (account_id, balance) VALUES ($1, 0)`, otherCash); err != nil {
		t.Fatalf("insert other balance: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO ledger_transactions (id, idempotency_key, type, status, amount, currency, source_account_id, destination_account_id) VALUES ($1, $2, 'transfer_p2p', 'posted', $3, 'USD', $4, $5)`,
		txID, "itest-"+txID.String(), amount, userCash, otherCash); err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, balance_after) VALUES ($1, $2, 'debit', $3, $4)`, txID, userCash, amount, 100000-amount); err != nil {
		t.Fatalf("insert debit entry: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO ledger_entries (transaction_id, account_id, direction, amount, balance_after) VALUES ($1, $2, 'credit', $3, $3)`, txID, otherCash, amount); err != nil {
		t.Fatalf("insert credit entry: %v", err)
	}
	return txID
}

func seedPublishedOutboxEvent(t *testing.T, db *sql.DB, txID, userID uuid.UUID, txType string, amount int64) {
	t.Helper()
	payload := fmt.Sprintf(`{"schema_version":1,"tx_id":"%s","transaction_type":"%s","amount":"%d","currency":"USD","entries":[],"occurred_at":"%s","user_id":"%s"}`,
		txID, txType, amount, time.Now().UTC().Format(time.RFC3339), userID)
	if _, err := db.Exec(`INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, status, published_at) VALUES (gen_random_uuid(), 'ledger_transaction', $1, 'ledger.transaction.posted.v1', $2::jsonb, 'published', now())`,
		txID, payload); err != nil {
		t.Fatalf("insert outbox event: %v", err)
	}
}

func TestReconstructPolicyCountersMatchesPostedTransactions(t *testing.T) {
	dsn, rdb := setupLedgerDB(t)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer db.Close()

	userID := uuid.New()
	txID := seedPostedTransfer(t, db, userID, 5000)
	seedPublishedOutboxEvent(t, db, txID, userID, "transfer_p2p", 5000)

	loc, _ := time.LoadLocation("Asia/Jakarta")
	report := &drreseed.Report{}
	counter := cache.NewRedisCounter(rdb)
	if err := drreseed.ReconstructPolicyCounters(context.Background(), db, counter, loc, report); err != nil {
		t.Fatalf("reconstruct policy counters: %v", err)
	}

	now := time.Now().In(loc)
	got, err := counter.Get(context.Background(), policy.DailyAmountKey(userID, "transfer_p2p", now))
	if err != nil {
		t.Fatalf("get daily amount: %v", err)
	}
	if got != 5000 {
		t.Fatalf("daily amount counter = %d, want 5000", got)
	}
}

func TestReconstructFraudVelocityFailsClosedOnMissingEvidence(t *testing.T) {
	dsn, rdb := setupLedgerDB(t)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer db.Close()

	userID := uuid.New()
	// A posted transaction with NO corresponding published outbox event —
	// the exact "missing source evidence" case K10 requires fail-closed
	// behavior for.
	seedPostedTransfer(t, db, userID, 1000)

	report := &drreseed.Report{}
	store := fraud.NewRedisVelocityStore(rdb)
	err = drreseed.ReconstructFraudVelocity(context.Background(), db, store, report)
	if err == nil {
		t.Fatal("expected ReconstructFraudVelocity to fail closed on missing evidence, got nil error")
	}
	if report.FraudEventsReplayed != 0 {
		t.Fatalf("expected zero events replayed when evidence is incomplete, got %d", report.FraudEventsReplayed)
	}
}
