//go:build integration

// Proves the §16.4/§25-item-7 finding's actual fix: tools/loadprobe's collect()
// correctly reports lock contention (a real blocked session, not a
// synthetic count) and — the point of the finding — completes fast enough
// to sustain the sub-second polling interval the harness runner now exposes
// (scripts/load-test.sh's SEEV_LOAD_PROBE_INTERVAL). A collector slower than
// the requested interval would silently degrade to coarser-than-requested
// sampling no matter what -interval says.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func setupProbeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	const dbName, dbUser, dbPassword = "seev_probe_test", "test", "secret"
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

	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(ctx))

	_, err = db.ExecContext(ctx, `CREATE TABLE probe_test (id INT PRIMARY KEY, balance BIGINT NOT NULL)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO probe_test (id, balance) VALUES (1, 1000)`)
	require.NoError(t, err)
	return db
}

// TestCollect_ReportsRealLockContention proves LockWaiting/LockWaitRelations
// reflect an ACTUAL blocked session, not just a query that happens to run —
// the exact signal B1-style experiments (docs/performance/reports/
// 2026-07-31-baseline.md §16.3/§16.4) depend on.
func TestCollect_ReportsRealLockContention(t *testing.T) {
	db := setupProbeTestDB(t)
	ctx := context.Background()

	holder, err := db.Conn(ctx)
	require.NoError(t, err)
	defer holder.Close()
	holderTx, err := holder.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = holderTx.Rollback() }()
	_, err = holderTx.ExecContext(ctx, `UPDATE probe_test SET balance = balance - 100 WHERE id = 1`)
	require.NoError(t, err)

	blockedDone := make(chan error, 1)
	go func() {
		blocked, connErr := db.Conn(ctx)
		if connErr != nil {
			blockedDone <- connErr
			return
		}
		defer blocked.Close()
		_, execErr := blocked.ExecContext(ctx, `UPDATE probe_test SET balance = balance + 50 WHERE id = 1`)
		blockedDone <- execErr
	}()

	// Give the blocked UPDATE time to actually start waiting before sampling.
	require.Eventually(t, func() bool {
		sample := collect(ctx, db)
		return sample.LockWaiting >= 1
	}, 5*time.Second, 20*time.Millisecond, "the blocked UPDATE never showed up as a lock wait")

	sample := collect(ctx, db)
	require.GreaterOrEqual(t, sample.LockWaiting, 1)
	// A row-level UPDATE/UPDATE conflict (session B updating the same row
	// session A already touched, uncommitted) blocks on session A's
	// TRANSACTION finishing — a transactionid lock, not a relation lock —
	// so pg_locks.relation is NULL and this collector reports it as
	// "unknown", not "probe_test". This is real, correct PostgreSQL
	// behavior for exactly the write-write row conflict shape a hot-account
	// balance UPDATE produces (docs/performance/reports/2026-07-31-baseline.md
	// §16.3/§16.4's B1 experiments), not a collector bug — confirmed live by
	// this test failing on the "probe_test" assumption first.
	require.NotEmpty(t, sample.LockWaitRelations, "a real blocked session must produce at least one relation breakdown row")
	found := false
	for _, r := range sample.LockWaitRelations {
		if r.Relation == "unknown" && r.Mode == "ShareLock" {
			found = true
			require.GreaterOrEqual(t, r.Count, 1)
		}
	}
	require.True(t, found, "expected an 'unknown'/ShareLock row (transactionid wait) for this row-conflict shape, got: %+v", sample.LockWaitRelations)

	require.NoError(t, holderTx.Commit())
	require.NoError(t, <-blockedDone)
}

// TestCollect_FastEnoughForSubSecondPolling proves the collector itself is
// not the bottleneck a fine SEEV_LOAD_PROBE_INTERVAL (e.g. 100ms) depends
// on — a slow collect() would silently turn a "100ms interval" request into
// coarser real sampling no matter what the flag says.
func TestCollect_FastEnoughForSubSecondPolling(t *testing.T) {
	db := setupProbeTestDB(t)
	ctx := context.Background()

	const samples = 20
	var total time.Duration
	var worst time.Duration
	for i := 0; i < samples; i++ {
		start := time.Now()
		sample := collect(ctx, db)
		elapsed := time.Since(start)
		total += elapsed
		if elapsed > worst {
			worst = elapsed
		}
		// pg_stat_statements needs shared_preload_libraries set at server
		// start (production's disposable Postgres sets this,
		// deploy/load/compose.load.yaml; a plain testcontainers instance
		// doesn't) — collect()'s own contract is to record that one
		// collector's failure and keep going, never to fail the whole
		// sample, so tolerate exactly that one here too.
		for _, e := range sample.Errors {
			require.Equal(t, "pg_stat_statements", e, "sample %d: unexpected collector error %+v", i, sample.Errors)
		}
	}
	mean := total / samples
	t.Logf("collect(): mean=%s worst=%s over %d samples", mean, worst, samples)
	require.Less(t, worst, 100*time.Millisecond, "collect() must comfortably fit inside a 100ms polling interval")
}
