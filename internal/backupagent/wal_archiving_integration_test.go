//go:build integration

// docs/roadmap/active/50 T2 Work item 5: prove WAL continues archiving without ever
// waiting for archive_timeout — this test forces rotation explicitly via
// pg_switch_wal() instead. It exercises plain PostgreSQL WAL archiving
// mechanics (a trivial local-copy archive_command, not pgBackRest itself
// — pgBackRest's own archive-push is exercised live against the real
// deploy/backup image in the isolated-Compose verification this task's
// Result section records) so it stays fast and needs no pgBackRest
// repository/secrets bootstrap to run in CI.
package backupagent_test

import (
	"context"
	"database/sql"
	"log"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestWALArchivingForcedRotation(t *testing.T) {
	ctx := context.Background()

	container, err := postgres.Run(ctx,
		"postgres:16.14-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("secret"),
		postgres.WithInitScripts(mustWriteArchiveDirScript(t)),
		postgres.BasicWaitStrategies(),
		testcontainers.WithCmd(
			"postgres",
			"-c", "wal_level=replica",
			"-c", "archive_mode=on",
			"-c", "archive_command=test ! -f /var/lib/postgresql/data/wal-archive/%f && cp %p /var/lib/postgresql/data/wal-archive/%f",
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			log.Printf("terminate container: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	var archivedBefore int64
	if err := db.QueryRowContext(ctx, "SELECT archived_count FROM pg_stat_archiver").Scan(&archivedBefore); err != nil {
		t.Fatalf("query archived_count before rotation: %v", err)
	}

	// Generate a little WAL so there is something to rotate, then force
	// the rotation directly — this is the "does not wait for
	// archive_timeout" requirement: no sleep, no polling a timer, just an
	// explicit pg_switch_wal() call.
	if _, err := db.ExecContext(ctx, "CREATE TABLE wal_probe (id int); INSERT INTO wal_probe VALUES (1)"); err != nil {
		t.Fatalf("generate WAL: %v", err)
	}
	var switchedLSN string
	if err := db.QueryRowContext(ctx, "SELECT pg_switch_wal()::text").Scan(&switchedLSN); err != nil {
		t.Fatalf("pg_switch_wal: %v", err)
	}
	t.Logf("forced WAL switch at LSN %s", switchedLSN)

	deadline := time.Now().Add(15 * time.Second)
	var archivedAfter int64
	var lastArchivedWAL string
	for {
		if err := db.QueryRowContext(ctx, "SELECT archived_count, coalesce(last_archived_wal, '') FROM pg_stat_archiver").Scan(&archivedAfter, &lastArchivedWAL); err != nil {
			t.Fatalf("query archived_count after rotation: %v", err)
		}
		if archivedAfter > archivedBefore {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("WAL segment was not archived within 15s of a forced pg_switch_wal() (archived_count stayed at %d)", archivedBefore)
		}
		time.Sleep(200 * time.Millisecond)
	}

	t.Logf("archived_count %d -> %d, last_archived_wal=%s", archivedBefore, archivedAfter, lastArchivedWAL)
	if lastArchivedWAL == "" {
		t.Fatal("last_archived_wal is empty after a successful-looking archive")
	}
}

// mustWriteArchiveDirScript writes a tiny init script (run by the
// official postgres image's docker-entrypoint.sh after initdb, before
// the final foreground start) that pre-creates the archive_command's
// target directory with the right ownership — archive_command itself
// cannot mkdir -p its own destination.
func mustWriteArchiveDirScript(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/00-wal-archive-dir.sh"
	script := "#!/bin/sh\nset -eu\nmkdir -p /var/lib/postgresql/data/wal-archive\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write init script: %v", err)
	}
	return path
}
