//go:build integration

// Package repository_test proves internal/merchant/repository's tenant
// isolation and race-safety guarantees against a real PostgreSQL, per
// docs/roadmap/active/57-c1-merchant-b2b-api.md T2's acceptance: "Repository
// integration tests use real PostgreSQL" and "Unique constraints prove
// race safety."
package repository_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/database"
)

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations")
}

// setupGatewayTestDB boots one Postgres container holding TWO databases —
// seev_ledger and seev_gateway — and applies ledger's migrations FIRST,
// even though this package needs none of ledger's tables: roles are
// cluster-wide, not per-database, and migrations/ledger/000009_rls_roles.up.sql
// is where app_service/app_readonly are actually created (`CREATE ROLE
// app_service` guarded by an existence check). Every gateway migration's
// own `GRANT ... TO app_service` fails against a fresh container without
// this — same two-step order internal/notify's own integration test
// already established for exactly this reason ("Ledger runs first because
// it creates the shared roles", testutil.ApplyServiceMigrations's own
// comment).
func setupGatewayTestDB(t *testing.T) *database.DBSQL {
	t.Helper()
	ctx := context.Background()

	container, err := pgcontainer.Run(ctx,
		"postgres:16.14-alpine",
		pgcontainer.WithDatabase("seev_ledger"),
		pgcontainer.WithUsername("test"),
		pgcontainer.WithPassword("secret"),
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
		Host: host, Port: port.Port(), User: "test", Password: "secret",
		DB: "seev_ledger", SSLMode: "disable", MaxOpenConns: 1,
	}).Pkg())
	require.NoError(t, err)
	_, err = adminDB.ExecContext(ctx, `CREATE DATABASE seev_gateway`)
	require.NoError(t, err)
	require.NoError(t, adminDB.Close())

	dsn := fmt.Sprintf("postgres://test:secret@%s:%s/seev_gateway?sslmode=disable", host, port.Port())
	require.NoError(t, testutil.ApplyMigration(migrationsSourceURL(t), "gateway", dsn))

	cfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: "test", Password: "secret",
		DB: "seev_gateway", SSLMode: "disable", MaxOpenConns: 10,
	}
	db, err := database.New(ctx, cfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTenant(t *testing.T, repo repository.TenantRepository, environment string) model.Tenant {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	tenant := model.Tenant{
		ID:              id,
		PublicID:        "mrc_" + id.String()[:16],
		ExternalCode:    "code-" + id.String(),
		Name:            "Test Merchant",
		Environment:     environment,
		Status:          "active",
		DefaultCurrency: "IDR",
		CreatedBy:       "repository_integration_test",
	}
	require.NoError(t, repo.Create(ctx, tenant))
	return tenant
}

func TestTenantRepository_CreateAndGet(t *testing.T) {
	db := setupGatewayTestDB(t)
	repo := repository.NewTenantRepository(db)
	tenant := newTenant(t, repo, "sandbox")

	got, err := repo.GetByID(context.Background(), tenant.ID)
	require.NoError(t, err)
	require.Equal(t, tenant.PublicID, got.PublicID)
	require.Equal(t, "active", got.Status)

	_, err = repo.GetByID(context.Background(), uuid.New())
	require.ErrorIs(t, err, repository.ErrNotFound)
}

// TestAPIKeyRepository_PrefixUniqueness_RaceSafe proves the
// UNIQUE(public_prefix) constraint — T2 acceptance "unique constraints
// prove race safety" — by firing N concurrent inserts of the SAME prefix
// and asserting exactly one wins.
func TestAPIKeyRepository_PrefixUniqueness_RaceSafe(t *testing.T) {
	db := setupGatewayTestDB(t)
	tenantRepo := repository.NewTenantRepository(db)
	keyRepo := repository.NewAPIKeyRepository(db)
	tenant := newTenant(t, tenantRepo, "sandbox")

	const prefix = "sk_test_racecondition"
	const concurrency = 10
	var wg sync.WaitGroup
	var successes int64
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k := model.APIKey{
				ID:           uuid.New(),
				PublicID:     "key_" + uuid.NewString()[:16],
				TenantID:     tenant.ID,
				PublicPrefix: prefix,
				SecretDigest: []byte("digest"),
				Environment:  "sandbox",
				Status:       "active",
				CreatedBy:    "race_test",
			}
			if err := keyRepo.Create(context.Background(), k); err == nil {
				atomic.AddInt64(&successes, 1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, successes, "exactly one concurrent insert of the same public_prefix must succeed")

	keys, err := keyRepo.ListByTenant(context.Background(), tenant.ID)
	require.NoError(t, err)
	require.Len(t, keys, 1)
}

func TestAPIKeyRepository_GetActiveByPrefix_IncludesScopes(t *testing.T) {
	db := setupGatewayTestDB(t)
	tenantRepo := repository.NewTenantRepository(db)
	keyRepo := repository.NewAPIKeyRepository(db)
	tenant := newTenant(t, tenantRepo, "live")

	k := model.APIKey{
		ID:           uuid.New(),
		PublicID:     "key_" + uuid.NewString()[:16],
		TenantID:     tenant.ID,
		PublicPrefix: "sk_live_scopedkey",
		SecretDigest: []byte("digest"),
		Environment:  "live",
		Status:       "active",
		CreatedBy:    "scope_test",
		Scopes:       []string{"accounts:read", "transfers:write"},
	}
	require.NoError(t, keyRepo.Create(context.Background(), k))

	got, err := keyRepo.GetActiveByPrefix(context.Background(), "sk_live_scopedkey")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"accounts:read", "transfers:write"}, got.Scopes)

	require.NoError(t, keyRepo.Revoke(context.Background(), tenant.ID, k.ID, "operator"))
	_, err = keyRepo.GetActiveByPrefix(context.Background(), "sk_live_scopedkey")
	require.ErrorIs(t, err, repository.ErrNotFound, "a revoked key must not resolve as active")
}

func TestAPIKeyRepository_Revoke_TenantScoped_CannotRevokeAnotherTenantsKey(t *testing.T) {
	db := setupGatewayTestDB(t)
	tenantRepo := repository.NewTenantRepository(db)
	keyRepo := repository.NewAPIKeyRepository(db)
	tenantA := newTenant(t, tenantRepo, "sandbox")
	tenantB := newTenant(t, tenantRepo, "sandbox")

	k := model.APIKey{
		ID: uuid.New(), PublicID: "key_" + uuid.NewString()[:16], TenantID: tenantA.ID,
		PublicPrefix: "sk_test_tenantscope", SecretDigest: []byte("d"), Environment: "sandbox",
		Status: "active", CreatedBy: "tenant_scope_test",
	}
	require.NoError(t, keyRepo.Create(context.Background(), k))

	// §7.3: a repository call scoped to the WRONG tenant must fail as
	// not-found, not silently revoke another tenant's key.
	err := keyRepo.Revoke(context.Background(), tenantB.ID, k.ID, "operator")
	require.ErrorIs(t, err, repository.ErrNotFound)

	keys, err := keyRepo.ListByTenant(context.Background(), tenantA.ID)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Equal(t, "active", keys[0].Status, "key must remain active after a cross-tenant revoke attempt")
}

// TestIdempotencyRepository_TenantScopedUniqueness_RaceSafe proves T4's
// "no tenant can collide with another tenant's idempotency key" AND the
// race-safety of concurrent same-key claims (T4 acceptance: "concurrent
// same-key same-body requests produce one owner operation").
func TestIdempotencyRepository_TenantScopedUniqueness_RaceSafe(t *testing.T) {
	db := setupGatewayTestDB(t)
	tenantRepo := repository.NewTenantRepository(db)
	idemRepo := repository.NewIdempotencyRepository(db)
	tenantA := newTenant(t, tenantRepo, "sandbox")
	tenantB := newTenant(t, tenantRepo, "sandbox")

	const key = "client-generated-key-123"
	const op = "b2bCreateTransferV1"

	// Two DIFFERENT tenants using the identical idempotency key string must
	// both succeed independently — tenant_id is part of the unique key.
	claimedA, _, err := idemRepo.Claim(context.Background(), model.IdempotencyRecord{
		ID: uuid.New(), TenantID: tenantA.ID, OperationID: op, IdempotencyKey: key,
		RequestHash: []byte("hashA"), DownstreamKey: "dsA", ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	require.True(t, claimedA)

	claimedB, _, err := idemRepo.Claim(context.Background(), model.IdempotencyRecord{
		ID: uuid.New(), TenantID: tenantB.ID, OperationID: op, IdempotencyKey: key,
		RequestHash: []byte("hashB"), DownstreamKey: "dsB", ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	require.True(t, claimedB, "a different tenant's identical idempotency key string must claim independently")

	// Concurrent claims of the SAME (tenant, operation, key) — exactly one wins.
	const concurrency = 10
	var wg sync.WaitGroup
	var successes int64
	const raceKey = "race-key-456"
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, _, err := idemRepo.Claim(context.Background(), model.IdempotencyRecord{
				ID: uuid.New(), TenantID: tenantA.ID, OperationID: op, IdempotencyKey: raceKey,
				RequestHash: []byte("same-hash"), DownstreamKey: "ds", ExpiresAt: time.Now().Add(time.Hour),
			})
			require.NoError(t, err)
			if claimed {
				atomic.AddInt64(&successes, 1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, successes, "exactly one concurrent claim of the same idempotency key must succeed")
}

func TestIdempotencyRepository_CompleteAndFail(t *testing.T) {
	db := setupGatewayTestDB(t)
	tenantRepo := repository.NewTenantRepository(db)
	idemRepo := repository.NewIdempotencyRepository(db)
	tenant := newTenant(t, tenantRepo, "sandbox")

	id := uuid.New()
	claimed, _, err := idemRepo.Claim(context.Background(), model.IdempotencyRecord{
		ID: id, TenantID: tenant.ID, OperationID: "op", IdempotencyKey: "k1",
		RequestHash: []byte("h"), DownstreamKey: "ds", ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	require.True(t, claimed)

	resourceID := "res-1"
	require.NoError(t, idemRepo.Complete(context.Background(), tenant.ID, id, 201, []byte(`{"ok":true}`), []byte(`{}`), &resourceID))

	got, err := idemRepo.GetByKey(context.Background(), tenant.ID, "op", "k1")
	require.NoError(t, err)
	require.Equal(t, "completed", got.State)
	require.NotNil(t, got.HTTPStatus)
	require.Equal(t, 201, *got.HTTPStatus)
}

// TestWebhookRepository_DeliveryUniqueness_RaceSafe proves T7's "one
// endpoint receives at most one automatic delivery record per event."
func TestWebhookRepository_DeliveryUniqueness_RaceSafe(t *testing.T) {
	db := setupGatewayTestDB(t)
	tenantRepo := repository.NewTenantRepository(db)
	webhookRepo := repository.NewWebhookRepository(db)
	tenant := newTenant(t, tenantRepo, "sandbox")

	endpoint := model.WebhookEndpoint{
		ID: uuid.New(), PublicID: "wh_" + uuid.NewString()[:16], TenantID: tenant.ID,
		URL: "https://example.invalid/hook", Status: "enabled",
		SecretCiphertext: []byte("ciphertext"), SecretVersion: 1,
		SubscribedEvents: []string{"transaction.posted.v1"},
	}
	require.NoError(t, webhookRepo.CreateEndpoint(context.Background(), endpoint))

	event := model.WebhookEvent{
		ID: uuid.New(), PublicID: "evt_" + uuid.NewString()[:16], TenantID: tenant.ID,
		EventType: "transaction.posted.v1", SchemaVersion: 1, Livemode: false,
		Payload: []byte(`{}`), PayloadBytes: []byte(`{}`), SourceEventID: uuid.New(),
	}
	require.NoError(t, webhookRepo.CreateEvent(context.Background(), event))

	const concurrency = 10
	var wg sync.WaitGroup
	var successes int64
	var winningID uuid.UUID
	var mu sync.Mutex
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := uuid.New()
			created, err := webhookRepo.CreateDelivery(context.Background(), model.WebhookDelivery{
				ID: id, PublicID: "wd_" + uuid.NewString()[:16], TenantID: tenant.ID,
				EndpointID: endpoint.ID, EventID: event.ID, Status: "pending",
			})
			require.NoError(t, err)
			if created {
				atomic.AddInt64(&successes, 1)
				mu.Lock()
				winningID = id
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, successes, "exactly one concurrent delivery-create for the same (endpoint,event) must succeed")

	// Replay bypasses the automatic path's uniqueness by design (T7): a
	// second, distinct row with the SAME event ID (and ReplayOfDeliveryID
	// pointing at the original) is expected to succeed.
	require.NoError(t, webhookRepo.CreateReplayDelivery(context.Background(), model.WebhookDelivery{
		ID: uuid.New(), PublicID: "wd_" + uuid.NewString()[:16], TenantID: tenant.ID,
		EndpointID: endpoint.ID, EventID: event.ID, Status: "pending", ReplayOfDeliveryID: &winningID,
	}))

	deliveries, err := webhookRepo.ListDeliveries(context.Background(), tenant.ID, 10)
	require.NoError(t, err)
	require.Len(t, deliveries, 2, "one automatic delivery + one replay delivery, both sharing the same event_id")
}

func TestWebhookRepository_AttemptsCascadeDeleteWithDelivery(t *testing.T) {
	db := setupGatewayTestDB(t)
	tenantRepo := repository.NewTenantRepository(db)
	webhookRepo := repository.NewWebhookRepository(db)
	tenant := newTenant(t, tenantRepo, "sandbox")

	endpoint := model.WebhookEndpoint{
		ID: uuid.New(), PublicID: "wh_" + uuid.NewString()[:16], TenantID: tenant.ID,
		URL: "https://example.invalid/hook", Status: "enabled",
		SecretCiphertext: []byte("c"), SecretVersion: 1, SubscribedEvents: []string{"transaction.posted.v1"},
	}
	require.NoError(t, webhookRepo.CreateEndpoint(context.Background(), endpoint))
	event := model.WebhookEvent{
		ID: uuid.New(), PublicID: "evt_" + uuid.NewString()[:16], TenantID: tenant.ID,
		EventType: "transaction.posted.v1", SchemaVersion: 1, Payload: []byte(`{}`),
		PayloadBytes: []byte(`{}`), SourceEventID: uuid.New(),
	}
	require.NoError(t, webhookRepo.CreateEvent(context.Background(), event))

	deliveryID := uuid.New()
	created, err := webhookRepo.CreateDelivery(context.Background(), model.WebhookDelivery{
		ID: deliveryID, PublicID: "wd_" + uuid.NewString()[:16], TenantID: tenant.ID,
		EndpointID: endpoint.ID, EventID: event.ID, Status: "pending",
	})
	require.NoError(t, err)
	require.True(t, created)

	require.NoError(t, webhookRepo.RecordAttempt(context.Background(), model.WebhookAttempt{
		ID: uuid.New(), DeliveryID: deliveryID, AttemptNumber: 1,
		StartedAt: time.Now(), FinishedAt: time.Now(), DurationMS: 10,
	}))

	// Deleting the delivery via a raw statement proves the schema's own
	// ON DELETE CASCADE (exercised for real here, not just declared in the
	// migration) removes its attempt rows too — this is what makes T2's
	// retention purge function for webhook_deliveries safe without also
	// needing its own attempts-cleanup step.
	_, err = db.ExecContext(context.Background(), `DELETE FROM merchant_webhook_deliveries WHERE id = $1`, deliveryID)
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM merchant_webhook_attempts WHERE delivery_id = $1`, deliveryID).Scan(&count))
	require.Zero(t, count, "attempts must cascade-delete with their parent delivery")
}
