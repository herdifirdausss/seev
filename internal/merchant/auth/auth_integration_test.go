//go:build integration

package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/merchant/auth"
	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/database"
)

const integrationTestPepper = "auth-integration-test-pepper"

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations")
}

// setupGatewayTestDB mirrors internal/merchant/repository's own helper —
// ledger migrations run first because that is where app_service/
// app_readonly are actually created (cluster-wide roles), a prerequisite
// every gateway migration's own GRANT statement depends on.
func setupGatewayTestDB(t *testing.T) *database.DBSQL {
	t.Helper()
	ctx := context.Background()

	container, err := pgcontainer.Run(ctx, "postgres:16.14-alpine",
		pgcontainer.WithDatabase("seev_ledger"), pgcontainer.WithUsername("test"), pgcontainer.WithPassword("secret"),
		pgcontainer.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5432")
	require.NoError(t, err)

	ledgerDSN := fmt.Sprintf("postgres://test:secret@%s:%s/seev_ledger?sslmode=disable", host, port.Port())
	require.NoError(t, testutil.ApplyMigration(migrationsSourceURL(t), "ledger", ledgerDSN))

	adminDB, err := database.New(ctx, (config.PostgresConfig{
		Host: host, Port: port.Port(), User: "test", Password: "secret", DB: "seev_ledger", SSLMode: "disable", MaxOpenConns: 1,
	}).Pkg())
	require.NoError(t, err)
	_, err = adminDB.ExecContext(ctx, `CREATE DATABASE seev_gateway`)
	require.NoError(t, err)
	require.NoError(t, adminDB.Close())

	dsn := fmt.Sprintf("postgres://test:secret@%s:%s/seev_gateway?sslmode=disable", host, port.Port())
	require.NoError(t, testutil.ApplyMigration(migrationsSourceURL(t), "gateway", dsn))

	db, err := database.New(ctx, (config.PostgresConfig{
		Host: host, Port: port.Port(), User: "test", Password: "secret", DB: "seev_gateway", SSLMode: "disable", MaxOpenConns: 10,
	}).Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestKeyServiceAndMiddleware_RealStack proves the full T3 lifecycle
// against real PostgreSQL: create a tenant + a key via KeyService, use
// the ACTUAL plaintext key over a real HTTP request through
// RequireMerchantAuth (backed by the real repository implementations,
// not fakes), then revoke and prove the same key immediately fails.
func TestKeyServiceAndMiddleware_RealStack(t *testing.T) {
	db := setupGatewayTestDB(t)
	tenants := repository.NewTenantRepository(db)
	keys := repository.NewAPIKeyRepository(db)
	keySvc := auth.NewKeyService(keys, integrationTestPepper)

	ctx := context.Background()
	tenantID := uuid.New()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantID, PublicID: "mrc_" + uuid.NewString()[:16], ExternalCode: "ext-" + tenantID.String(),
		Name: "Real Stack Co", Environment: "sandbox", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))

	plaintext, keyID, err := keySvc.CreateKey(ctx, tenantID, "sandbox", []string{"merchant:read"}, "operator")
	require.NoError(t, err)
	require.NotEmpty(t, plaintext)

	handler := auth.RequireMerchantAuth(keys, tenants, integrationTestPepper)(
		auth.RequireScope("b2bGetMerchantV1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := auth.PrincipalFromContext(r.Context())
			require.True(t, ok)
			require.Equal(t, tenantID, p.TenantID)
			require.Equal(t, keyID, p.KeyID)
			w.WriteHeader(http.StatusOK)
		})),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/b2b/merchant", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "a real key through the real repository-backed middleware must authenticate")

	// Scope denial: the same valid key, but a scope it was never granted.
	deniedHandler := auth.RequireMerchantAuth(keys, tenants, integrationTestPepper)(
		auth.RequireScope("b2bCreateTransferV1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("handler must not run: key lacks transfers:write/transactions:read")
		})),
	)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/b2b/transfers", nil)
	req2.Header.Set("Authorization", "Bearer "+plaintext)
	rec2 := httptest.NewRecorder()
	deniedHandler.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusForbidden, rec2.Code)

	// Revoke, then prove the exact same plaintext immediately fails.
	require.NoError(t, keySvc.RevokeKey(ctx, tenantID, keyID, "operator"))
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req)
	require.Equal(t, http.StatusUnauthorized, rec3.Code, "revocation must take effect immediately against the real database")
}

// TestKeyService_RotateKey_RealStack proves §8.4's rotation contract: the
// new key works, and the old key is revoked as part of the same
// operation — both facts proven against real Postgres, not a fake.
func TestKeyService_RotateKey_RealStack(t *testing.T) {
	db := setupGatewayTestDB(t)
	tenants := repository.NewTenantRepository(db)
	keys := repository.NewAPIKeyRepository(db)
	keySvc := auth.NewKeyService(keys, integrationTestPepper)

	ctx := context.Background()
	tenantID := uuid.New()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantID, PublicID: "mrc_" + uuid.NewString()[:16], ExternalCode: "ext-" + tenantID.String(),
		Name: "Rotation Co", Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))

	oldPlaintext, oldKeyID, err := keySvc.CreateKey(ctx, tenantID, "live", []string{"merchant:read"}, "operator")
	require.NoError(t, err)

	newPlaintext, newKeyID, err := keySvc.RotateKey(ctx, tenantID, oldKeyID, "live", []string{"merchant:read"}, "operator")
	require.NoError(t, err)
	require.NotEqual(t, oldKeyID, newKeyID)
	require.NotEqual(t, oldPlaintext, newPlaintext)

	handler := auth.RequireMerchantAuth(keys, tenants, integrationTestPepper)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	newReq := httptest.NewRequest(http.MethodGet, "/api/v1/b2b/merchant", nil)
	newReq.Header.Set("Authorization", "Bearer "+newPlaintext)
	newRec := httptest.NewRecorder()
	handler.ServeHTTP(newRec, newReq)
	require.Equal(t, http.StatusOK, newRec.Code, "the newly rotated key must authenticate")

	oldReq := httptest.NewRequest(http.MethodGet, "/api/v1/b2b/merchant", nil)
	oldReq.Header.Set("Authorization", "Bearer "+oldPlaintext)
	oldRec := httptest.NewRecorder()
	handler.ServeHTTP(oldRec, oldReq)
	require.Equal(t, http.StatusUnauthorized, oldRec.Code, "the old key must be revoked as part of rotation")
}

// TestKeyService_CreateKey_EnforcesMaxTwoActiveKeysPerEnvironment proves
// §8.4's "at most two active keys per environment" against real
// Postgres.
func TestKeyService_CreateKey_EnforcesMaxTwoActiveKeysPerEnvironment(t *testing.T) {
	db := setupGatewayTestDB(t)
	tenants := repository.NewTenantRepository(db)
	keys := repository.NewAPIKeyRepository(db)
	keySvc := auth.NewKeyService(keys, integrationTestPepper)

	ctx := context.Background()
	tenantID := uuid.New()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantID, PublicID: "mrc_" + uuid.NewString()[:16], ExternalCode: "ext-" + tenantID.String(),
		Name: "Limit Co", Environment: "sandbox", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))

	_, _, err := keySvc.CreateKey(ctx, tenantID, "sandbox", []string{"merchant:read"}, "operator")
	require.NoError(t, err)
	_, _, err = keySvc.CreateKey(ctx, tenantID, "sandbox", []string{"merchant:read"}, "operator")
	require.NoError(t, err)

	_, _, err = keySvc.CreateKey(ctx, tenantID, "sandbox", []string{"merchant:read"}, "operator")
	require.ErrorIs(t, err, auth.ErrTooManyActiveKeys)
}
