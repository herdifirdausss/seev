//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T1.7's gateway.notifications
// classes end to end against a real Postgres: eligibility boundary (read
// notifications need read_at set and 180d old; the .any backstop only
// needs 365d old regardless of read_at), dry-run parity, retention-hold
// exclusion, and direct DELETE still forbidden. Reuses setupNotifyTestDBs
// (notify_integration_test.go, same package) — it already applies ledger's
// migrations before gateway's on one Postgres container specifically so
// app_service/app_readonly (cluster-wide roles ledger's own migration
// creates) exist by the time gateway's GRANT statements run; this task's
// new retention functions need that same role to exist.
package notify_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/pkg/database"
)

func insertTestNotification(t *testing.T, db *database.DBSQL, id, userID uuid.UUID, readAt *time.Time, createdAt time.Time) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO notif_notifications (id, user_id, event_id, type, title, body, read_at, created_at)
		VALUES ($1, $2, $3, 'money_in', 'title', 'body', $4, $5)`,
		id, userID, uuid.New(), readAt, createdAt)
	require.NoError(t, err)
}

func countAllNotifications(t *testing.T, db *database.DBSQL) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT count(*) FROM notif_notifications`).Scan(&n))
	return n
}

func TestRetention_NotificationsRead_EligibilityBoundary(t *testing.T) {
	_, db := setupNotifyTestDBs(t)
	ctx := context.Background()
	userID := uuid.New()

	notYetReadAt := time.Now().Add(-179 * 24 * time.Hour)
	eligibleReadAt := time.Now().Add(-181 * 24 * time.Hour)
	insertTestNotification(t, db, uuid.New(), userID, &notYetReadAt, time.Now().Add(-200*24*time.Hour))
	insertTestNotification(t, db, uuid.New(), userID, &eligibleReadAt, time.Now().Add(-200*24*time.Hour))
	// Unread, old enough to matter for .any but never eligible under .read.
	insertTestNotification(t, db, uuid.New(), userID, nil, time.Now().Add(-200*24*time.Hour))

	var dryRun int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_notifications_read($1, 500, true)`, uuid.New()).Scan(&dryRun))
	require.Equal(t, 1, dryRun, "only the read-and-181d-old row should be eligible")

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_notifications_read($1, 500, false)`, uuid.New()).Scan(&affected))
	require.Equal(t, 1, affected)
	require.Equal(t, 2, countAllNotifications(t, db), "the not-yet-eligible and unread rows must survive")
}

func TestRetention_NotificationsAny_BackstopIgnoresReadState(t *testing.T) {
	_, db := setupNotifyTestDBs(t)
	ctx := context.Background()
	userID := uuid.New()

	insertTestNotification(t, db, uuid.New(), userID, nil, time.Now().Add(-366*24*time.Hour))
	insertTestNotification(t, db, uuid.New(), userID, nil, time.Now().Add(-364*24*time.Hour))

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_notifications_any($1, 500, false)`, uuid.New()).Scan(&affected))
	require.Equal(t, 1, affected, "only the 366d-old unread row crosses the 365d backstop")
	require.Equal(t, 1, countAllNotifications(t, db))
}

func TestRetention_Notifications_DryRunMatchesReal(t *testing.T) {
	_, db := setupNotifyTestDBs(t)
	ctx := context.Background()
	userID := uuid.New()
	old := time.Now().Add(-200 * 24 * time.Hour)
	for i := 0; i < 4; i++ {
		insertTestNotification(t, db, uuid.New(), userID, &old, old)
	}

	var dryRun int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_notifications_read($1, 500, true)`, uuid.New()).Scan(&dryRun))
	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_notifications_read($1, 500, false)`, uuid.New()).Scan(&affected))
	require.Equal(t, dryRun, affected)
	require.Equal(t, 4, affected)
}

func TestRetention_Notifications_RetentionHoldExcludesRow(t *testing.T) {
	_, db := setupNotifyTestDBs(t)
	ctx := context.Background()
	heldUser := uuid.New()
	old := time.Now().Add(-200 * 24 * time.Hour)
	insertTestNotification(t, db, uuid.New(), heldUser, &old, old)

	_, err := db.ExecContext(ctx, `
		INSERT INTO gateway_retention_holds (id, scope, scope_value, reason_code, created_by)
		VALUES ($1, 'subject', $2, 'legal_hold', 'tester')`, uuid.New(), heldUser.String())
	require.NoError(t, err)

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_notifications_read($1, 500, true)`, uuid.New()).Scan(&affected))
	require.Equal(t, 0, affected, "an active subject-scoped hold must exclude the row")
	require.Equal(t, 1, countAllNotifications(t, db))
}

func TestRetention_Notifications_DirectDeleteStillForbidden(t *testing.T) {
	ledgerDB, _, gatewayCfg := setupNotifyTestDBsWithConfig(t)
	ctx := context.Background()

	const appPassword = "app-test-pw"
	_, err := ledgerDB.ExecContext(ctx, `CREATE ROLE test_retention_app_service LOGIN PASSWORD '`+appPassword+`'`)
	require.NoError(t, err)
	_, err = ledgerDB.ExecContext(ctx, `GRANT app_service TO test_retention_app_service`)
	require.NoError(t, err)

	appCfg := gatewayCfg
	appCfg.User, appCfg.Password = "test_retention_app_service", appPassword
	appDB, err := database.New(ctx, appCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })

	_, err = appDB.ExecContext(ctx, `DELETE FROM notif_notifications WHERE id = gen_random_uuid()`)
	require.Error(t, err, "app_service must not be able to DELETE notif_notifications directly, only via the retention function")
}
