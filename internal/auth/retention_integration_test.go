//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T1's auth.refresh_tokens
// class end to end against a real Postgres: eligibility boundary, dry-run
// parity, retention-hold exclusion, and that app_service still cannot
// DELETE auth_refresh_tokens directly. Reuses setupAuthTestDB
// (auth_integration_test.go, same package) — safe to use the shared
// all-services test harness now that migrations/*/*_retention_holds are
// service-prefixed (a real collision this task found and fixed live, see
// internal/ledger/retention_integration_test.go's own comment).
package auth_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/database"
)

// insertTestUser satisfies auth_refresh_tokens.user_id's FK to auth_users.
// Goes through UserRepository (auth_users.email/full_name have no
// plaintext column since "A8 T2.5b"'s contract migration) rather than a
// raw INSERT — ON CONFLICT DO NOTHING at the SQL layer isn't available
// through CreateUser, so a pre-existing id is tolerated by ignoring
// ErrDuplicateEmail/a duplicate-key error, making this still safe to call
// once per userID across several token inserts in the same test.
func insertTestUser(t *testing.T, db *database.DBSQL, userID uuid.UUID) {
	t.Helper()
	repo := repository.NewUserRepository(db, testRing(t), testLookupKey(t))
	err := repo.CreateUser(context.Background(), model.User{
		ID: userID, Email: "retention-test-" + userID.String() + "@example.com",
		FullName: "Test User", Role: "user", Status: "active",
	}, "hash")
	if err != nil && !errors.Is(err, repository.ErrDuplicateEmail) {
		require.NoError(t, err)
	}
}

func insertRefreshToken(t *testing.T, db *database.DBSQL, id, userID uuid.UUID, expiresAt time.Time, revokedAt *time.Time) {
	t.Helper()
	insertTestUser(t, db, userID)
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO auth_refresh_tokens (id, user_id, token_hash, expires_at, created_at, revoked_at)
		VALUES ($1, $2, $3, $4, $4, $5)`,
		id, userID, "hash-"+id.String(), expiresAt, revokedAt)
	require.NoError(t, err)
}

func countRefreshTokens(t *testing.T, db *database.DBSQL) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT count(*) FROM auth_refresh_tokens`).Scan(&n))
	return n
}

func TestRetention_RefreshTokens_EligibilityBoundary(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()
	userID := uuid.New()

	notYetEligibleRevoked := time.Now().Add(-29 * 24 * time.Hour)
	eligibleRevoked := time.Now().Add(-31 * 24 * time.Hour)

	notYetEligible := uuid.New()
	eligible := uuid.New()
	insertRefreshToken(t, db, notYetEligible, userID, time.Now().Add(48*time.Hour), &notYetEligibleRevoked)
	insertRefreshToken(t, db, eligible, userID, time.Now().Add(48*time.Hour), &eligibleRevoked)

	var dryRun int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_refresh_tokens($1, 500, true)`, uuid.New()).Scan(&dryRun))
	require.Equal(t, 1, dryRun, "only the token revoked more than 30 days ago should be eligible")

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_refresh_tokens($1, 500, false)`, uuid.New()).Scan(&affected))
	require.Equal(t, 1, affected)
	require.Equal(t, 1, countRefreshTokens(t, db), "the not-yet-eligible token must survive")
}

func TestRetention_RefreshTokens_LiveTokenNeverEligible(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()
	userID := uuid.New()
	live := uuid.New()
	insertRefreshToken(t, db, live, userID, time.Now().Add(48*time.Hour), nil)

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_refresh_tokens($1, 500, true)`, uuid.New()).Scan(&affected))
	require.Equal(t, 0, affected, "a live (unrevoked, unexpired) token must never be eligible")
}

func TestRetention_RefreshTokens_DryRunMatchesReal(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()
	userID := uuid.New()
	old := time.Now().Add(-60 * 24 * time.Hour)
	for i := 0; i < 4; i++ {
		insertRefreshToken(t, db, uuid.New(), userID, time.Now().Add(48*time.Hour), &old)
	}

	var dryRun int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_refresh_tokens($1, 500, true)`, uuid.New()).Scan(&dryRun))

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_refresh_tokens($1, 500, false)`, uuid.New()).Scan(&affected))

	require.Equal(t, dryRun, affected)
	require.Equal(t, 4, affected)
}

func TestRetention_RefreshTokens_RetentionHoldExcludesRow(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()
	heldUser := uuid.New()
	old := time.Now().Add(-60 * 24 * time.Hour)
	tokenID := uuid.New()
	insertRefreshToken(t, db, tokenID, heldUser, time.Now().Add(48*time.Hour), &old)

	_, err := db.ExecContext(ctx, `
		INSERT INTO auth_retention_holds (id, scope, scope_value, reason_code, created_by)
		VALUES ($1, 'subject', $2, 'legal_hold', 'tester')`, uuid.New(), heldUser.String())
	require.NoError(t, err)

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_refresh_tokens($1, 500, true)`, uuid.New()).Scan(&affected))
	require.Equal(t, 0, affected, "an active subject-scoped hold must exclude the row")
	require.Equal(t, 1, countRefreshTokens(t, db))
}

func TestRetention_RefreshTokens_DirectDeleteStillForbidden(t *testing.T) {
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

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPassword, host, port.Port(), dbName)
	// app_service/app_readonly are cluster-wide Postgres roles, created only
	// by ledger's own migration (000009_rls_roles.up.sql) — ApplyServiceMigrations
	// runs ledger first for exactly this reason (see its own doc comment).
	// A single-service ApplyMigration("auth", ...) would skip that step
	// entirely and fail with "role app_service does not exist".
	require.NoError(t, testutil.ApplyServiceMigrations(migrationsSourceURL(t), dsn))

	ownerCfg := config.PostgresConfig{Host: host, Port: port.Port(), User: dbUser, Password: dbPassword, DB: dbName, SSLMode: "disable", MaxOpenConns: 10}
	ownerDB, err := database.New(ctx, ownerCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = ownerDB.Close() })

	const appPassword = "app-test-pw"
	_, err = ownerDB.ExecContext(ctx, `CREATE ROLE test_retention_app_service LOGIN PASSWORD '`+appPassword+`'`)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, `GRANT app_service TO test_retention_app_service`)
	require.NoError(t, err)

	appCfg := ownerCfg
	appCfg.User, appCfg.Password = "test_retention_app_service", appPassword
	appDB, err := database.New(ctx, appCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })

	_, err = appDB.ExecContext(ctx, `DELETE FROM auth_refresh_tokens`)
	require.Error(t, err, "app_service must not be able to DELETE auth_refresh_tokens directly, only via the retention function")
}

// ─── T1.7: auth.kyc_apply_retries.succeeded ────────────────────────────────
// kyc_apply_retries.dead is deliberately not covered here — see
// migrations/auth/000009_retention_purge_kyc_apply_retries.up.sql's own
// comment: it's blocked on an audit-summary mechanism that doesn't exist
// in this codebase yet, not implemented, and so has nothing to test.

func insertKYCApplyRetry(t *testing.T, db *database.DBSQL, userID uuid.UUID, status string, updatedAt time.Time) uuid.UUID {
	t.Helper()
	insertTestUser(t, db, userID)
	submissionID := uuid.New()
	require.NoError(t, repository.NewKYCRepository(db, cryptoxTestRing).CreateKYCSubmission(context.Background(), model.KYCSubmission{
		ID: submissionID, UserID: userID, LevelRequested: 1, Provider: "test", Payload: map[string]any{},
	}))
	_, err := db.ExecContext(context.Background(), `UPDATE kyc_submissions SET status = 'approved' WHERE id = $1`, submissionID)
	require.NoError(t, err)
	retryID := uuid.New()
	_, err = db.ExecContext(context.Background(), `
		INSERT INTO kyc_apply_retries (id, submission_id, user_id, level, status, updated_at)
		VALUES ($1, $2, $3, 1, $4, $5)`, retryID, submissionID, userID, status, updatedAt)
	require.NoError(t, err)
	return retryID
}

func countKYCApplyRetries(t *testing.T, db *database.DBSQL) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT count(*) FROM kyc_apply_retries`).Scan(&n))
	return n
}

func TestRetention_KYCApplyRetriesSucceeded_EligibilityBoundary(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()

	notYet := time.Now().Add(-89 * 24 * time.Hour)
	eligible := time.Now().Add(-91 * 24 * time.Hour)
	insertKYCApplyRetry(t, db, uuid.New(), "succeeded", notYet)
	insertKYCApplyRetry(t, db, uuid.New(), "succeeded", eligible)
	// A pending retry old enough to matter for age but never eligible —
	// status must gate independently of updated_at.
	insertKYCApplyRetry(t, db, uuid.New(), "pending", eligible)

	var dryRun int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_kyc_apply_retries_succeeded($1, 500, true)`, uuid.New()).Scan(&dryRun))
	require.Equal(t, 1, dryRun, "only the succeeded-and-91d-old retry should be eligible")

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_kyc_apply_retries_succeeded($1, 500, false)`, uuid.New()).Scan(&affected))
	require.Equal(t, 1, affected)
	require.Equal(t, 2, countKYCApplyRetries(t, db), "the not-yet-eligible succeeded retry and the pending retry must survive")
}

func TestRetention_KYCApplyRetriesSucceeded_DryRunMatchesReal(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()
	old := time.Now().Add(-120 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		insertKYCApplyRetry(t, db, uuid.New(), "succeeded", old)
	}

	var dryRun int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_kyc_apply_retries_succeeded($1, 500, true)`, uuid.New()).Scan(&dryRun))
	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_kyc_apply_retries_succeeded($1, 500, false)`, uuid.New()).Scan(&affected))
	require.Equal(t, dryRun, affected)
	require.Equal(t, 3, affected)
}

func TestRetention_KYCApplyRetriesSucceeded_RetentionHoldExcludesRow(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()
	heldUser := uuid.New()
	old := time.Now().Add(-120 * 24 * time.Hour)
	insertKYCApplyRetry(t, db, heldUser, "succeeded", old)

	_, err := db.ExecContext(ctx, `
		INSERT INTO auth_retention_holds (id, scope, scope_value, reason_code, created_by)
		VALUES ($1, 'subject', $2, 'legal_hold', 'tester')`, uuid.New(), heldUser.String())
	require.NoError(t, err)

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_kyc_apply_retries_succeeded($1, 500, true)`, uuid.New()).Scan(&affected))
	require.Equal(t, 0, affected, "an active subject-scoped hold must exclude the row")
	require.Equal(t, 1, countKYCApplyRetries(t, db))
}

func TestRetention_KYCApplyRetries_DirectDeleteStillForbidden(t *testing.T) {
	ownerDB, ownerCfg := setupAuthTestDBWithConfig(t)
	ctx := context.Background()

	const appPassword = "app-test-pw2"
	_, err := ownerDB.ExecContext(ctx, `CREATE ROLE test_retention_app_service2 LOGIN PASSWORD '`+appPassword+`'`)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, `GRANT app_service TO test_retention_app_service2`)
	require.NoError(t, err)

	appCfg := ownerCfg
	appCfg.User, appCfg.Password = "test_retention_app_service2", appPassword
	appDB, err := database.New(ctx, appCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })

	_, err = appDB.ExecContext(ctx, `DELETE FROM kyc_apply_retries WHERE id = gen_random_uuid()`)
	require.Error(t, err, "app_service must not be able to DELETE kyc_apply_retries directly, only via the retention function")
}
