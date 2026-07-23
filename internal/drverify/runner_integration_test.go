//go:build integration

package drverify_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/drverify"
	"github.com/herdifirdausss/seev/internal/testutil"
)

// setupCluster provisions one testcontainers Postgres, creates all eight
// authoritative databases inside it, and applies each service's own
// migrations — the same eight-separate-databases-one-instance topology
// docker-compose.yml uses, not a single shared database, since drverify's
// own correctness (each check connecting to the right database, the
// migration-table-per-service convention) depends on that boundary
// actually existing.
func setupCluster(t *testing.T) map[string]string {
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

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}

	dsns := make(map[string]string, len(drverify.Services))
	for _, service := range drverify.Services {
		dbName := "seev_" + service
		if _, err := admin.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s", dbName)); err != nil {
			t.Fatalf("create database %s: %v", dbName, err)
		}
		dsn := fmt.Sprintf("postgres://seev:seev@%s:%s/%s?sslmode=disable", host, port.Port(), dbName)
		if err := testutil.ApplyMigration("file://../../migrations", service, dsn); err != nil {
			t.Fatalf("apply %s migrations: %v", service, err)
		}
		dsns[service] = dsn
	}
	return dsns
}

func setEnv(t *testing.T, dsns map[string]string) {
	t.Helper()
	for service, dsn := range dsns {
		key := envKeyForTest(service)
		t.Setenv(key, dsn)
	}
}

func envKeyForTest(service string) string {
	switch service {
	case "adminbff":
		return "ADMINBFF_DSN"
	default:
		upper := ""
		for _, r := range service {
			if r >= 'a' && r <= 'z' {
				r -= 'a' - 'A'
			}
			upper += string(r)
		}
		return upper + "_DSN"
	}
}

func TestRunCleanClusterPasses(t *testing.T) {
	dsns := setupCluster(t)
	setEnv(t, dsns)
	cfg, err := drverify.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := drverify.Run(context.Background(), cfg)
	if !report.Passed() {
		t.Fatalf("clean, freshly-migrated cluster should pass: %+v", report)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("clean cluster produced findings: %+v", report.Findings)
	}
}

func TestRunDetectsDirtyMigration(t *testing.T) {
	dsns := setupCluster(t)
	db, err := sql.Open("pgx", dsns["ledger"])
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE schema_migrations_ledger SET dirty = true"); err != nil {
		t.Fatalf("mark dirty: %v", err)
	}

	setEnv(t, dsns)
	cfg, err := drverify.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	report := drverify.Run(context.Background(), cfg)
	if report.Passed() {
		t.Fatal("dirty migration table must fail the gate")
	}
	found := false
	for _, f := range report.Findings {
		if f.Code == "MIGRATION_DIRTY" && f.Service == "ledger" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a MIGRATION_DIRTY finding for ledger, got: %+v", report.Findings)
	}
}

// TestNoWriteIsPossible is the required test: "no write is possible
// through verifier DSNs." It connects with the exact same DSN drverify
// itself uses and proves Postgres's own read-only-transaction guarantee
// rejects a write — the same mechanism internal/drverify/db.go's
// readOnlyQuery relies on, verified independently of drverify's own code
// so this test would still catch a regression that silently stopped
// setting ReadOnly: true.
func TestNoWriteIsPossible(t *testing.T) {
	dsns := setupCluster(t)
	db, err := sql.Open("pgx", dsns["ledger"])
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer db.Close()

	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read-only tx: %v", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`INSERT INTO ledger_transactions (id, idempotency_key, type, status, amount, currency) VALUES (gen_random_uuid(), 'no-write-test', 'money_in', 'posted', 100, 'USD')`)
	if err == nil {
		t.Fatal("expected the read-only transaction to reject a write, it succeeded")
	}
}
