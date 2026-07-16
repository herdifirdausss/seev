//go:build integration

// Package auth_test drives internal/auth.Module end to end against a real
// ledger.Module and real Postgres (docs/plan/25 Task T1) — proves the whole
// vertical: register -> credentials persisted -> ledger accounts
// provisioned -> issued JWT passes the EXISTING middleware -> login/refresh
// rotation against real rows.
package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/auth"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

const testJWTSecretIT = "supersecretkeythatisatleast32chars!"

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}

func setupAuthTestDB(t *testing.T) *database.DBSQL {
	t.Helper()
	ctx := context.Background()

	const dbName, dbUser, dbPassword = "seev_test", "test", "secret"

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
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
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	}
	db, err := database.New(ctx, cfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

func newAuthModule(db *database.DBSQL) (*auth.Module, *testutil.LedgerHarness) {
	ledgerModule := testutil.NewLedgerHarness(db)
	authModule := auth.NewModule(db, ledgerModule, auth.Config{
		JWTSecret: testJWTSecretIT, JWTIssuer: "seev-test",
		AccessExpiry: 15 * time.Minute, RefreshExpiry: 7 * 24 * time.Hour,
		DefaultCurrency: "IDR",
	}, nil)
	return authModule, ledgerModule
}

// TestAuth_RegisterLoginMe_EndToEnd proves the full onboarding vertical.
func TestAuth_RegisterLoginMe_EndToEnd(t *testing.T) {
	db := setupAuthTestDB(t)
	m, ledgerModule := newAuthModule(db)
	ctx := context.Background()

	u, pair, err := m.Register(ctx, "alice@example.com", "hunter22!", "Alice A")
	require.NoError(t, err)

	// 1. The issued JWT must pass the EXISTING auth middleware untouched.
	var gotUserID string
	protected := middleware.WithAuth(testJWTSecretIT, "seev-test")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUserID = middleware.UserIDFromCtx(r.Context())
			w.WriteHeader(http.StatusOK)
		}))
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "issued JWT must pass pkg/middleware.WithAuth")
	assert.Equal(t, u.ID.String(), gotUserID)

	// 2. Ledger accounts were provisioned by Register.
	accounts, err := ledgerModule.ListAccounts(ctx, u.ID)
	require.NoError(t, err)
	types := map[string]bool{}
	for _, a := range accounts {
		types[a.Type] = true
	}
	for _, want := range []string{"cash", "hold", "pending", "frozen"} {
		assert.True(t, types[want], "account type %q must be provisioned on register", want)
	}

	// 3. Login works against the real bcrypt hash.
	got, pair2, err := m.Login(ctx, "ALICE@example.com", "hunter22!") // case-insensitive email
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	assert.NotEmpty(t, pair2.RefreshToken)

	// 4. Me returns the profile.
	me, err := m.Me(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alice A", me.FullName)
}

func TestAuth_DuplicateEmail_409_CaseInsensitive(t *testing.T) {
	db := setupAuthTestDB(t)
	m, _ := newAuthModule(db)
	ctx := context.Background()

	_, _, err := m.Register(ctx, "bob@example.com", "hunter22!", "")
	require.NoError(t, err)

	_, _, err = m.Register(ctx, "BOB@Example.Com", "different1!", "")
	assert.ErrorIs(t, err, auth.ErrEmailTaken)
}

// TestAuth_RefreshRotation_RealRows proves rotation + replay containment
// against real DB rows, not mocks.
func TestAuth_RefreshRotation_RealRows(t *testing.T) {
	db := setupAuthTestDB(t)
	m, _ := newAuthModule(db)
	ctx := context.Background()

	_, pair, err := m.Register(ctx, "carol@example.com", "hunter22!", "")
	require.NoError(t, err)

	// First refresh: succeeds, rotates.
	_, pair2, err := m.Refresh(ctx, pair.RefreshToken)
	require.NoError(t, err)
	require.NotEqual(t, pair.RefreshToken, pair2.RefreshToken)

	// Replaying the ORIGINAL (now-revoked) token must fail AND revoke the chain.
	_, _, err = m.Refresh(ctx, pair.RefreshToken)
	assert.ErrorIs(t, err, auth.ErrInvalidRefreshToken)

	// The successor from the legitimate rotation is now ALSO dead (chain nuked).
	_, _, err = m.Refresh(ctx, pair2.RefreshToken)
	assert.ErrorIs(t, err, auth.ErrInvalidRefreshToken,
		"replay containment must revoke the whole chain, including the newest token")
}

func TestAuth_BootstrapAdmin_Idempotent(t *testing.T) {
	db := setupAuthTestDB(t)
	m, _ := newAuthModule(db)
	ctx := context.Background()

	require.NoError(t, m.EnsureBootstrapAdmin(ctx, "root@example.com", "super-secret-pass"))
	require.NoError(t, m.EnsureBootstrapAdmin(ctx, "root@example.com", "super-secret-pass"))

	u, pair, err := m.Login(ctx, "root@example.com", "super-secret-pass")
	require.NoError(t, err)
	assert.Equal(t, "admin", u.Role)

	claims, err := middleware.ParseToken(testJWTSecretIT, pair.AccessToken, "seev-test")
	require.NoError(t, err)
	assert.Equal(t, "admin", claims.Role, "bootstrap admin's JWT must carry the admin role")
}
