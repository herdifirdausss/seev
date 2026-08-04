//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T1.7's assurance.runs.succeeded
// and assurance.alert_deliveries classes end to end against a real
// Postgres: eligibility boundary, dry-run parity, and direct DELETE still
// forbidden. Neither class has hold_scope (T0's own classification — a
// pipeline run or alert delivery has no subject/resource a hold could ever
// target), so unlike every other owner's retention integration test in
// this repo, there is no hold-exclusion test here — matches the two
// migrations' own functions, which never call fn_assurance_retention_hold_covers.
//
// Applies ledger's migrations before assurance's on one Postgres container
// (same reasoning as services/gateway/internal/notification/inbox/notify_integration_test.go's
// setupNotifyTestDBs): app_service/app_readonly are cluster-wide roles
// created only by ledger's own migration, and assurance's retention
// functions' GRANT EXECUTE ... TO app_service needs that role to already
// exist.
package assurance_test

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

	"github.com/herdifirdausss/seev/internal/platform/config"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/testkit"
)

func assuranceMigrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
}

func setupAssuranceTestDB(t *testing.T) *database.DBSQL {
	t.Helper()
	db, _ := setupAssuranceTestDBWithConfig(t)
	return db
}

func setupAssuranceTestDBWithConfig(t *testing.T) (*database.DBSQL, config.PostgresConfig) {
	t.Helper()
	ctx := context.Background()

	const ledgerDBName, assuranceDBName = "seev_ledger", "seev_assurance"
	const dbUser, dbPassword = "test", "secret"

	container, err := postgres.Run(ctx,
		"postgres:16.14-alpine",
		postgres.WithDatabase(ledgerDBName),
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

	ledgerConfig := config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: ledgerDBName, SSLMode: "disable", MaxOpenConns: 5,
	}
	ledgerDB, err := database.New(ctx, ledgerConfig.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = ledgerDB.Close() })
	_, err = ledgerDB.ExecContext(ctx, `CREATE DATABASE `+assuranceDBName)
	require.NoError(t, err)

	ledgerDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPassword, host, port.Port(), ledgerDBName)
	assuranceDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPassword, host, port.Port(), assuranceDBName)
	require.NoError(t, testutil.ApplyMigration(assuranceMigrationsSourceURL(t), "ledger", ledgerDSN))
	require.NoError(t, testutil.ApplyMigration(assuranceMigrationsSourceURL(t), "assurance", assuranceDSN))

	assuranceConfig := ledgerConfig
	assuranceConfig.DB = assuranceDBName
	assuranceDB, err := database.New(ctx, assuranceConfig.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = assuranceDB.Close() })
	return assuranceDB, assuranceConfig
}

func insertAssuranceRun(t *testing.T, db *database.DBSQL, status string, finishedAt *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO assurance_runs (id, mode, status, started_at, finished_at)
		VALUES ($1, 'incremental', $2, now(), $3)`, id, status, finishedAt)
	require.NoError(t, err)
	return id
}

func countAssuranceRuns(t *testing.T, db *database.DBSQL) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT count(*) FROM assurance_runs`).Scan(&n))
	return n
}

func TestRetention_RunsSucceeded_EligibilityBoundary(t *testing.T) {
	db := setupAssuranceTestDB(t)
	ctx := context.Background()

	notYet := time.Now().Add(-89 * 24 * time.Hour)
	eligible := time.Now().Add(-91 * 24 * time.Hour)
	insertAssuranceRun(t, db, "succeeded", &notYet)
	insertAssuranceRun(t, db, "succeeded", &eligible)
	// A failed run old enough to matter for .succeeded's age window but
	// never eligible under it — status must gate independently of age.
	insertAssuranceRun(t, db, "failed", &eligible)

	var dryRun int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_runs_succeeded($1, 500, true)`, uuid.New()).Scan(&dryRun))
	require.Equal(t, 1, dryRun, "only the succeeded-and-91d-old run should be eligible")

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_runs_succeeded($1, 500, false)`, uuid.New()).Scan(&affected))
	require.Equal(t, 1, affected)
	require.Equal(t, 2, countAssuranceRuns(t, db), "the not-yet-eligible succeeded run and the failed run must survive")
}

func TestRetention_RunsSucceeded_DryRunMatchesReal(t *testing.T) {
	db := setupAssuranceTestDB(t)
	ctx := context.Background()
	old := time.Now().Add(-120 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		insertAssuranceRun(t, db, "succeeded", &old)
	}

	var dryRun int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_runs_succeeded($1, 500, true)`, uuid.New()).Scan(&dryRun))
	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_runs_succeeded($1, 500, false)`, uuid.New()).Scan(&affected))
	require.Equal(t, dryRun, affected)
	require.Equal(t, 3, affected)
}

// TestRetention_RunsSucceeded_SkipsRowStillReferencedByCursor proves the
// schema audit fix (services/assurance/migrations/000009): a succeeded run still
// referenced by assurance_cursors.updated_by_run_id must survive purge
// (and must NOT abort the whole batch on the FK constraint, the failure
// mode before the NOT EXISTS guard was added) — a source that stops
// scanning for 90+ days would otherwise leave its cursor pointing at a
// row the very next retention cycle tries to delete.
func TestRetention_RunsSucceeded_SkipsRowStillReferencedByCursor(t *testing.T) {
	db := setupAssuranceTestDB(t)
	ctx := context.Background()

	eligible := time.Now().Add(-91 * 24 * time.Hour)
	referenced := insertAssuranceRun(t, db, "succeeded", &eligible)
	unreferenced := insertAssuranceRun(t, db, "succeeded", &eligible)

	_, err := db.ExecContext(ctx, `
		INSERT INTO assurance_cursors (source, updated_by_run_id) VALUES ('payin', $1)
		ON CONFLICT (source) DO UPDATE SET updated_by_run_id = EXCLUDED.updated_by_run_id`, referenced)
	require.NoError(t, err)

	var dryRun int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_runs_succeeded($1, 500, true)`, uuid.New()).Scan(&dryRun))
	require.Equal(t, 1, dryRun, "only the unreferenced eligible run should count")

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_runs_succeeded($1, 500, false)`, uuid.New()).Scan(&affected))
	require.Equal(t, 1, affected)

	var stillThere int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM assurance_runs WHERE id = $1`, referenced).Scan(&stillThere))
	require.Equal(t, 1, stillThere, "the cursor-referenced run must survive")

	var deleted int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM assurance_runs WHERE id = $1`, unreferenced).Scan(&deleted))
	require.Zero(t, deleted, "the unreferenced eligible run must be purged")
}

func TestRetention_AlertDeliveries_TerminalStateOnly(t *testing.T) {
	db := setupAssuranceTestDB(t)
	ctx := context.Background()

	findingID := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO assurance_findings (id, fingerprint, severity, rule_code, resource_id, currency, status)
		VALUES ($1, $2, 'high', 'PA01', 'res-1', 'IDR', 'open')`, findingID, findingID.String())
	require.NoError(t, err)

	old := time.Now().Add(-200 * 24 * time.Hour)
	_, err = db.ExecContext(ctx, `
		INSERT INTO assurance_alert_deliveries (id, finding_id, severity, message, status, next_attempt_at, delivered_at)
		VALUES ($1, $2, 'high', 'm', 'delivered', $3, $3)`, uuid.New(), findingID, old)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO assurance_alert_deliveries (id, finding_id, severity, message, status, next_attempt_at)
		VALUES ($1, $2, 'high', 'm', 'pending', $3)`, uuid.New(), findingID, old)
	require.NoError(t, err)

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_alert_deliveries($1, 500, true)`, uuid.New()).Scan(&affected))
	require.Equal(t, 1, affected, "only the terminal 'delivered' row is eligible — 'pending' must never be purged regardless of age")
}

func TestRetention_AssuranceDirectDeleteStillForbidden(t *testing.T) {
	db, cfg := setupAssuranceTestDBWithConfig(t)
	ctx := context.Background()

	const appPassword = "app-test-pw"
	_, err := db.ExecContext(ctx, `CREATE ROLE test_retention_app_service LOGIN PASSWORD '`+appPassword+`'`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `GRANT app_service TO test_retention_app_service`)
	require.NoError(t, err)

	appCfg := cfg
	appCfg.User, appCfg.Password = "test_retention_app_service", appPassword
	appDB, err := database.New(ctx, appCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })

	_, err = appDB.ExecContext(ctx, `DELETE FROM assurance_runs WHERE id = gen_random_uuid()`)
	require.Error(t, err, "app_service must not be able to DELETE assurance_runs directly, only via the retention function")
}
