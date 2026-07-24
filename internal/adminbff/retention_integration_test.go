//go:build integration

// Proves docs/roadmap/active/51-a8-data-lifecycle-privacy.md T1.5's
// adminbff.sessions class end to end against a real Postgres: eligibility
// boundary (7-day grace past GREATEST(expires_at, absolute_expires_at)),
// dry-run parity, retention-hold exclusion, and that app_service still
// cannot DELETE sessions directly. Uses the full testutil.ApplyServiceMigrations
// harness (not a single-service migration) — app_service/app_readonly are
// cluster-wide Postgres roles created only by ledger's own migration
// (000009_rls_roles.up.sql), which a single-service adminbff migration
// would skip entirely (see internal/auth/retention_integration_test.go's
// setupAuthTestDB, same reasoning). This differs from
// internal/ledger/retention_integration_test.go's setupLedgerOnlyDB, which
// can use a single-service migration precisely because ledger IS the
// migration set that creates those roles.
package adminbff_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
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

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}

func setupAdminBFFOnlyDB(t *testing.T) *database.DBSQL {
	t.Helper()
	db, _ := setupAdminBFFOnlyDBWithConfig(t)
	return db
}

func setupAdminBFFOnlyDBWithConfig(t *testing.T) (*database.DBSQL, config.PostgresConfig) {
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
	require.NoError(t, testutil.ApplyServiceMigrations(migrationsSourceURL(t), dsn))

	cfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 20,
	}
	db, err := database.New(ctx, cfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, cfg
}

func insertTestSession(t *testing.T, db *database.DBSQL, id string, userID uuid.UUID, expiresAt, absoluteExpiresAt time.Time) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO sessions (id, user_id, email, role, csrf_token, created_at, last_seen_at, expires_at, absolute_expires_at)
		VALUES ($1, $2, $3, 'admin', 'csrf', now(), now(), $4, $5)`,
		id, userID, id+"@example.test", expiresAt, absoluteExpiresAt)
	require.NoError(t, err)
}

func countSessions(t *testing.T, db *database.DBSQL) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT count(*) FROM sessions`).Scan(&n))
	return n
}

func TestRetention_Sessions_EligibilityBoundary(t *testing.T) {
	db := setupAdminBFFOnlyDB(t)
	ctx := context.Background()

	notYetEligible := "sess-not-yet"
	eligible := "sess-eligible"
	insertTestSession(t, db, notYetEligible, uuid.New(), time.Now().Add(-6*24*time.Hour), time.Now().Add(-6*24*time.Hour))
	insertTestSession(t, db, eligible, uuid.New(), time.Now().Add(-7*24*time.Hour-time.Second), time.Now().Add(-7*24*time.Hour-time.Second))

	var dryRun int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_sessions($1, 500, true)`, uuid.New()).Scan(&dryRun))
	require.Equal(t, 1, dryRun, "only the session past the 7-day grace period should be eligible")

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_sessions($1, 500, false)`, uuid.New()).Scan(&affected))
	require.Equal(t, 1, affected)
	require.Equal(t, 1, countSessions(t, db), "the not-yet-eligible session must survive")
}

func TestRetention_Sessions_DryRunMatchesReal(t *testing.T) {
	db := setupAdminBFFOnlyDB(t)
	ctx := context.Background()
	old := time.Now().Add(-30 * 24 * time.Hour)
	for i := 0; i < 4; i++ {
		insertTestSession(t, db, fmt.Sprintf("sess-old-%d", i), uuid.New(), old, old)
	}

	var dryRun int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_sessions($1, 500, true)`, uuid.New()).Scan(&dryRun))

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_sessions($1, 500, false)`, uuid.New()).Scan(&affected))

	require.Equal(t, dryRun, affected)
	require.Equal(t, 4, affected)
}

func TestRetention_Sessions_RetentionHoldExcludesRow(t *testing.T) {
	db := setupAdminBFFOnlyDB(t)
	ctx := context.Background()
	held := "sess-held"
	insertTestSession(t, db, held, uuid.New(), time.Now().Add(-30*24*time.Hour), time.Now().Add(-30*24*time.Hour))

	_, err := db.ExecContext(ctx, `
		INSERT INTO adminbff_retention_holds (id, scope, scope_value, reason_code, created_by)
		VALUES ($1, 'resource', $2, 'legal_hold', 'tester')`, uuid.New(), held)
	require.NoError(t, err)

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_sessions($1, 500, true)`, uuid.New()).Scan(&affected))
	require.Equal(t, 0, affected, "an active resource-scoped hold must exclude the row")
	require.Equal(t, 1, countSessions(t, db))
}

func TestRetention_Sessions_DirectDeleteStillForbidden(t *testing.T) {
	ownerDB, ownerCfg := setupAdminBFFOnlyDBWithConfig(t)
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

	_, err = appDB.ExecContext(ctx, `DELETE FROM sessions`)
	require.Error(t, err, "app_service must not be able to DELETE sessions directly, only via the retention function")
}

// TestRetention_Runner_EndToEnd proves pkg/retentionworker.Runner wired
// against fn_retention_purge_sessions the same way internal/adminbff/module.go's
// Start() wires it in production: dry-run then real-run through the shared
// Go abstraction, and that adminbff_retention_audit rows land as K4 requires.
func TestRetention_Runner_EndToEnd(t *testing.T) {
	db := setupAdminBFFOnlyDB(t)
	ctx := context.Background()
	old := time.Now().Add(-30 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		insertTestSession(t, db, fmt.Sprintf("sess-runner-%d", i), uuid.New(), old, old)
	}

	runner, err := retentionworker.NewRunner("adminbff", db, []retentionworker.Class{
		{Name: "adminbff.sessions", Action: "delete", FunctionName: "fn_retention_purge_sessions"},
	})
	require.NoError(t, err)

	dryReport := runner.RunOnce(ctx, true)
	require.NoError(t, dryReport.Classes["adminbff.sessions"].Err)
	require.Equal(t, 3, dryReport.Classes["adminbff.sessions"].Affected)

	realReport := runner.RunOnce(ctx, false)
	require.NoError(t, realReport.Classes["adminbff.sessions"].Err)
	require.Equal(t, 3, realReport.Classes["adminbff.sessions"].Affected)
	require.Equal(t, 0, countSessions(t, db))

	var auditCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM adminbff_retention_audit WHERE class = 'adminbff.sessions'`).Scan(&auditCount))
	require.Equal(t, 2, auditCount, "one audit row for the dry run, one for the real run")
}
