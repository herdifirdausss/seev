//go:build integration

// Package adminbff proves the sessions DELETE fix (migrations/adminbff/
// 000002_session_delete_fn.up.sql) actually works against a real Postgres
// role that only holds the app_service grant — the same role adminbff_app
// connects as in every real deployment path (docker-compose.yml,
// scripts/lib.sh ensure_app_role). Before this fix, DeleteSession and
// CleanupSessions issued a direct DELETE, which app_service has never been
// granted (migrations/adminbff/000001_core.up.sql line 35 grants only
// SELECT, INSERT, UPDATE) and failed with "permission denied for table
// sessions".
package adminbff

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
)

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}

// appServiceSessionTestDB bundles the schema-owner connection (used to
// create the throwaway login role) and a connection through a fresh LOGIN
// role granted ONLY app_service — mirroring what adminbff_app actually is
// in every real deployment (scripts/lib.sh ensure_app_role grants
// app_service, never DELETE directly).
type appServiceSessionTestDB struct {
	ownerDB *database.DBSQL
	appDB   *database.DBSQL
}

func setupAppServiceSessionTestDB(t *testing.T) appServiceSessionTestDB {
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

	// ApplyServiceMigrations runs ledger first (it creates the shared
	// app_service/app_readonly roles) then every other service's migrations,
	// including adminbff's — the same ordering the real stack uses
	// (scripts/lib.sh apply_migrations).
	require.NoError(t, testutil.ApplyServiceMigrations(migrationsSourceURL(t), dsn))

	ownerCfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	}
	ownerDB, err := database.New(ctx, ownerCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = ownerDB.Close() })

	const appPassword = "adminbff-app-test-pw"
	_, err = ownerDB.ExecContext(ctx, `CREATE ROLE test_adminbff_app LOGIN PASSWORD '`+appPassword+`'`)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, `GRANT app_service TO test_adminbff_app`)
	require.NoError(t, err)

	appCfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: "test_adminbff_app", Password: appPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	}
	appDB, err := database.New(ctx, appCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })

	return appServiceSessionTestDB{ownerDB: ownerDB, appDB: appDB}
}

func seedSession(t *testing.T, repo SessionRepository, id string) {
	t.Helper()
	now := time.Now()
	require.NoError(t, repo.CreateSession(context.Background(), Session{
		ID: id, UserID: uuid.New(), Email: "operator@example.test", Role: "admin",
		CSRFToken: "csrf", CreatedAt: now, LastSeenAt: now,
		ExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour),
	}))
}

// TestSchemaContract_AppServiceRole_CannotDeleteSessionDirectly proves the
// underlying privilege gap this bug relied on is real and still enforced —
// app_service must keep going through fn_delete_session, not gain a direct
// DELETE grant.
func TestSchemaContract_AppServiceRole_CannotDeleteSessionDirectly(t *testing.T) {
	dbs := setupAppServiceSessionTestDB(t)
	ctx := context.Background()

	repo := NewSessionRepository(dbs.appDB)
	seedSession(t, repo, "direct-delete-session")

	_, err := dbs.appDB.ExecContext(ctx, `DELETE FROM sessions WHERE id = $1`, "direct-delete-session")
	require.Error(t, err)
	require.Contains(t, err.Error(), "permission denied")
}

// TestSchemaContract_AppServiceRole_DeleteSessionRemovesRow proves logout's
// path (SessionRepository.DeleteSession -> fn_delete_session) actually
// removes the row under the app_service role, not just returns success.
func TestSchemaContract_AppServiceRole_DeleteSessionRemovesRow(t *testing.T) {
	dbs := setupAppServiceSessionTestDB(t)
	ctx := context.Background()

	repo := NewSessionRepository(dbs.appDB)
	seedSession(t, repo, "logout-session")

	require.NoError(t, repo.DeleteSession(ctx, "logout-session"))

	var count int
	require.NoError(t, dbs.ownerDB.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE id = $1`, "logout-session").Scan(&count))
	require.Equal(t, 0, count)

	// Deleting an id that no longer exists must stay a no-op, matching the
	// original DELETE ... WHERE id = $1 semantics (no error on zero rows).
	require.NoError(t, repo.DeleteSession(ctx, "logout-session"))
}

// TestSchemaContract_AppServiceRole_CleanupSessionsRemovesOnlyExpired proves
// the periodic cleanup job (module.go Start's cron) removes rows past their
// own expiry columns under app_service and leaves live sessions untouched.
func TestSchemaContract_AppServiceRole_CleanupSessionsRemovesOnlyExpired(t *testing.T) {
	dbs := setupAppServiceSessionTestDB(t)
	ctx := context.Background()

	repo := NewSessionRepository(dbs.appDB)
	past := time.Now().Add(-time.Hour)
	require.NoError(t, repo.CreateSession(ctx, Session{
		ID: "expired-session", UserID: uuid.New(), Email: "operator@example.test", Role: "admin",
		CSRFToken: "csrf", CreatedAt: past, LastSeenAt: past,
		ExpiresAt: past.Add(time.Minute), AbsoluteExpiresAt: past.Add(time.Minute),
	}))
	seedSession(t, repo, "live-session")

	require.NoError(t, repo.CleanupSessions(ctx, time.Now()))

	var expiredCount, liveCount int
	require.NoError(t, dbs.ownerDB.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE id = $1`, "expired-session").Scan(&expiredCount))
	require.Equal(t, 0, expiredCount)
	require.NoError(t, dbs.ownerDB.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE id = $1`, "live-session").Scan(&liveCount))
	require.Equal(t, 1, liveCount)
}
