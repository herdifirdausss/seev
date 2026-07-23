//go:build integration

package database_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/herdifirdausss/seev/pkg/database"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

var testDB *database.DBSQL
var ctx context.Context
var testCfg database.Config // base config, reused by tests that need their own connection (e.g. session GUCs)

func TestMain(m *testing.M) {

	var err error
	ctx = context.Background()

	dbName := "testdb"
	dbUser := "test"
	dbPassword := "secret"

	container, err := postgres.Run(ctx,
		"postgres:16.14-alpine",
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		postgres.BasicWaitStrategies(),
	)

	if err != nil {
		log.Fatalf("failed to start container: %s", err)
	}
	host, err := container.Host(ctx)
	if err != nil {
		panic(err)
	}

	port, err := container.MappedPort(ctx, "5432")
	if err != nil {
		panic(err)
	}

	connStr, _ := container.ConnectionString(ctx, "sslmode=disable")
	fmt.Println("connection string postgres : ", connStr)

	testCfg = database.Config{
		Host:         host,
		Port:         port.Port(),
		User:         dbUser,
		Password:     dbPassword,
		DB:           dbName,
		SSLMode:      "disable",
		MaxOpenConns: 100,
	}

	testDB, err = database.New(ctx, testCfg)
	if err != nil {
		panic(err)
	}

	code := m.Run()

	testDB.Close()
	container.Terminate(ctx)

	os.Exit(code)
}

func TestNew_FailPing(t *testing.T) {
	ctx := context.Background()

	configPostgres := database.Config{Host: "localhost", Port: "5432", User: "test", Password: "random", DB: "testdb", SSLMode: "disable"}
	_, err := database.New(ctx, configPostgres)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExecQueryAndRow(t *testing.T) {
	db := testDB

	_, err := db.ExecContext(ctx, `
		CREATE TABLE users(
			id SERIAL PRIMARY KEY,
			name TEXT
		)
	`)
	if err != nil {
		t.Fatal(err)
	}

	res, err := db.ExecContext(ctx, `INSERT INTO users(name) VALUES($1)`, "john")
	if err != nil {
		t.Fatal(err)
	}

	aff, _ := res.RowsAffected()
	if aff != 1 {
		t.Fatal("insert failed")
	}

	row := db.QueryRowContext(ctx, `SELECT name FROM users WHERE id=$1`, 1)

	var name string
	if err := row.Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "john" {
		t.Fatalf("unexpected name %s", name)
	}

	rows, err := db.QueryContext(ctx, `SELECT name FROM users`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("expected row")
	}
}

func TestWithTx_Commit(t *testing.T) {
	db := testDB

	err := db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		_, err := tx.Exec(`SELECT 1`)
		return err
	})

	if err != nil {
		t.Fatal(err)
	}
}

func TestWithTx_RollbackOnError(t *testing.T) {
	db := testDB

	expected := errors.New("fail")

	err := db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return expected
	})

	if !errors.Is(err, expected) {
		t.Fatalf("expected %v got %v", expected, err)
	}
}

func TestWithTx_RollbackOnPanic(t *testing.T) {
	db := testDB

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()

	_ = db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		panic("boom")
	})
}

func TestStatsAndClose(t *testing.T) {
	db := testDB

	stats := db.Stats()
	if stats.MaxOpenConnections == 0 {
		t.Fatal("stats not populated")
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// after close ping should fail
	if err := db.HealthCheck(ctx); err == nil {
		t.Fatal("expected error after close")
	}
}

func TestPaginate(t *testing.T) {
	q, limit, offset := database.Paginate("SELECT * FROM x", 0, 0, 50)

	if limit != 50 {
		t.Fatal("limit fallback failed")
	}
	if offset != 0 {
		t.Fatal("offset wrong")
	}
	if q == "" {
		t.Fatal("query empty")
	}

	q, limit, offset = database.Paginate("SELECT * FROM x", 2, 10, 50)

	if limit != 10 || offset != 10 {
		t.Fatal("pagination calc wrong")
	}
}

// TestNew_SessionTimeouts_AppliedViaDSN proves the statement_timeout/
// lock_timeout/idle_in_transaction_session_timeout GUCs configured in
// PostgresConfig actually reach the server (docs/roadmap/archive/11 Task T5) — this is
// exactly the kind of DSN-string-construction behavior that can silently be
// wrong (typo'd keyword, bad quoting) without ever failing to connect, so it
// must be verified against real Postgres, not asserted from the Go string
// alone. Uses its own connection — independent of the shared testDB, which
// TestStatsAndClose above has already closed by the time tests run in file
// order.
func TestNew_SessionTimeouts_AppliedViaDSN(t *testing.T) {
	cfg := testCfg
	cfg.StatementTimeout = 1234 * time.Millisecond
	cfg.LockTimeout = 2345 * time.Millisecond
	cfg.IdleInTxTimeout = 3456 * time.Millisecond

	db, err := database.New(ctx, cfg)
	if err != nil {
		t.Fatalf("connect with session timeouts: %v", err)
	}
	defer db.Close()

	assertGUCMillis := func(name string, want int) {
		t.Helper()
		var got string
		row := db.QueryRowContext(ctx, `SELECT setting FROM pg_settings WHERE name = $1`, name)
		if err := row.Scan(&got); err != nil {
			t.Fatalf("query pg_settings %s: %v", name, err)
		}
		if got != fmt.Sprintf("%d", want) {
			t.Fatalf("%s: expected %d, got %s", name, want, got)
		}
	}

	assertGUCMillis("statement_timeout", 1234)
	assertGUCMillis("lock_timeout", 2345)
	assertGUCMillis("idle_in_transaction_session_timeout", 3456)
}

// TestNew_LockTimeout_ActuallyAborts proves lock_timeout isn't just a GUC
// that's set but never enforced — a second connection blocked on a row lock
// held by the first must abort with the expected Postgres error once
// lock_timeout elapses, rather than hanging until the test times out. The
// pool has plenty of headroom (testCfg.MaxOpenConns=100) so the holder and
// the blocked UPDATE get distinct physical connections, same as two
// concurrent requests would in production.
func TestNew_LockTimeout_ActuallyAborts(t *testing.T) {
	cfg := testCfg
	cfg.LockTimeout = 200 * time.Millisecond

	db, err := database.New(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS lock_timeout_test (id INT PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO lock_timeout_test (id) VALUES (1) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	lockAcquired := make(chan struct{})
	releaseHolder := make(chan struct{})
	holderErrCh := make(chan error, 1)

	go func() {
		holderErrCh <- db.WithTx(ctx, nil, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `SELECT * FROM lock_timeout_test WHERE id = 1 FOR UPDATE`); err != nil {
				return err
			}
			close(lockAcquired)
			<-releaseHolder
			return nil
		})
	}()

	select {
	case <-lockAcquired:
	case <-time.After(5 * time.Second):
		t.Fatal("holder never acquired the row lock")
	}

	start := time.Now()
	_, updateErr := db.ExecContext(ctx, `UPDATE lock_timeout_test SET id = 1 WHERE id = 1`)
	elapsed := time.Since(start)

	close(releaseHolder)
	if err := <-holderErrCh; err != nil {
		t.Fatalf("holder tx failed: %v", err)
	}

	if updateErr == nil {
		t.Fatal("expected lock_timeout error, got nil")
	}
	if !strings.Contains(updateErr.Error(), "lock") {
		t.Fatalf("expected a lock-timeout-flavored error, got: %v", updateErr)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("blocked for %s — lock_timeout=200ms was not enforced", elapsed)
	}
}
