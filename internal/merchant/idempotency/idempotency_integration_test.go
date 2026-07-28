//go:build integration

package idempotency_test

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
	"github.com/herdifirdausss/seev/internal/merchant/idempotency"
	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/database"
)

// newTenant mirrors internal/merchant/repository's own test helper —
// merchant_idempotency_records.tenant_id carries a real foreign key to
// merchant_tenants, so every integration test below must claim against an
// actually-persisted tenant, not a bare uuid.New().
func newTenant(t *testing.T, repo repository.TenantRepository) model.Tenant {
	t.Helper()
	id := uuid.New()
	tenant := model.Tenant{
		ID: id, PublicID: "mrc_" + id.String()[:16], ExternalCode: "code-" + id.String(),
		Name: "Idempotency Test Merchant", Environment: "sandbox", Status: "active",
		DefaultCurrency: "IDR", CreatedBy: "idempotency_integration_test",
	}
	require.NoError(t, repo.Create(context.Background(), tenant))
	return tenant
}

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations")
}

// setupGatewayTestDB mirrors internal/merchant/repository's and
// internal/merchant/auth's own helper — ledger migrations run first
// because that is where app_service/app_readonly are actually created
// (cluster-wide roles), a prerequisite every gateway migration's own
// GRANT statement depends on.
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
		Host: host, Port: port.Port(), User: "test", Password: "secret", DB: "seev_gateway", SSLMode: "disable", MaxOpenConns: 20,
	}).Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestService_Begin_ConcurrentSameKeySameBody_RealPostgres proves T4's own
// acceptance criterion — "concurrent same-key same-body requests produce
// one owner operation" — against the REAL repository/database, not the
// in-memory fake used in idempotency_test.go. Only one of N concurrent
// Begin calls may observe OutcomeNew; every other must be OutcomeInProgress.
func TestService_Begin_ConcurrentSameKeySameBody_RealPostgres(t *testing.T) {
	db := setupGatewayTestDB(t)
	repo := repository.NewIdempotencyRepository(db)
	svc := idempotency.NewService(repo, time.Hour, "integration-test-owner")

	tenantID := newTenant(t, repository.NewTenantRepository(db)).ID
	body := []byte(`{"amount":100,"currency":"IDR"}`)

	const n = 25
	var newCount, inProgressCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			decision, err := svc.Begin(context.Background(), tenantID, "b2bCreateTransferV1", "shared-key", body)
			require.NoError(t, err)
			switch decision.Outcome {
			case idempotency.OutcomeNew:
				newCount.Add(1)
			case idempotency.OutcomeInProgress:
				inProgressCount.Add(1)
			default:
				t.Errorf("unexpected outcome %d against real Postgres", decision.Outcome)
			}
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), newCount.Load(), "exactly one concurrent claim must win against the real UNIQUE(tenant_id, operation_id, idempotency_key) constraint")
	require.Equal(t, int32(n-1), inProgressCount.Load())
}

// TestService_Begin_SameKeyDifferentBody_RealPostgres proves
// IDEMPOTENCY_KEY_REUSED against the real repository.
func TestService_Begin_SameKeyDifferentBody_RealPostgres(t *testing.T) {
	db := setupGatewayTestDB(t)
	repo := repository.NewIdempotencyRepository(db)
	svc := idempotency.NewService(repo, time.Hour, "integration-test-owner")

	tenantID := newTenant(t, repository.NewTenantRepository(db)).ID
	_, err := svc.Begin(context.Background(), tenantID, "b2bCreateTransferV1", "reused-key", []byte(`{"amount":100}`))
	require.NoError(t, err)

	decision, err := svc.Begin(context.Background(), tenantID, "b2bCreateTransferV1", "reused-key", []byte(`{"amount":999}`))
	require.NoError(t, err)
	require.Equal(t, idempotency.OutcomeConflict, decision.Outcome)
}

// TestService_Begin_CrashRecovery_RealPostgres proves T4's own "Gateway
// crash after downstream success does not duplicate money" acceptance
// criterion end-to-end through the Service: Begin (claim), simulate a
// process crash BEFORE Complete is ever called (the lease is left
// dangling), then prove a retry after the lease naturally expires
// reclaims the SAME record and SAME downstream key rather than creating a
// second one — and that once Complete finally runs, every subsequent
// retry replays the stored response instead of re-executing.
func TestService_Begin_CrashRecovery_RealPostgres(t *testing.T) {
	db := setupGatewayTestDB(t)
	repo := repository.NewIdempotencyRepository(db)
	svc := idempotency.NewService(repo, time.Hour, "process-a")

	tenantID := newTenant(t, repository.NewTenantRepository(db)).ID
	body := []byte(`{"amount":250}`)

	first, err := svc.Begin(context.Background(), tenantID, "b2bCreateTransferV1", "crash-key", body)
	require.NoError(t, err)
	require.Equal(t, idempotency.OutcomeNew, first.Outcome)

	// Process A "crashes" here: never calls Complete or Fail. Force the
	// lease into the past directly against Postgres to simulate the
	// leaseDuration window having elapsed, then let a second process
	// (svc2) recover it.
	rec, err := repo.GetByKey(context.Background(), tenantID, "b2bCreateTransferV1", "crash-key")
	require.NoError(t, err)
	took, err := repo.TakeoverExpiredLease(context.Background(), tenantID, rec.ID, "process-b", time.Now().Add(time.Hour))
	require.False(t, took, "a lease that has not yet expired must not be reclaimable")

	// Directly age the lease to prove the recovery path (matches how a
	// real leaseDuration elapsing would look from the database's view).
	_, err = db.ExecContext(context.Background(),
		`UPDATE merchant_idempotency_records SET lease_expires_at = now() - interval '1 minute' WHERE id = $1`, rec.ID)
	require.NoError(t, err)

	svc2 := idempotency.NewService(repo, time.Hour, "process-b")
	recovered, err := svc2.Begin(context.Background(), tenantID, "b2bCreateTransferV1", "crash-key", body)
	require.NoError(t, err)
	require.Equal(t, idempotency.OutcomeNew, recovered.Outcome, "an expired lease must be recoverable by a second process")
	require.Equal(t, first.RecordID, recovered.RecordID, "recovery must reuse the SAME record, never create a duplicate")
	require.Equal(t, first.DownstreamKey, recovered.DownstreamKey, "recovery must reuse the SAME downstream key — this is what prevents the downstream owner service from seeing two different operations for one logical request")

	require.NoError(t, svc2.Complete(context.Background(), tenantID, recovered.RecordID, 201, []byte(`{"id":"tx_recovered"}`), nil, nil))

	replay, err := svc2.Begin(context.Background(), tenantID, "b2bCreateTransferV1", "crash-key", body)
	require.NoError(t, err)
	require.Equal(t, idempotency.OutcomeReplay, replay.Outcome, "once completed, every later retry must replay rather than re-execute")
}

// TestService_Begin_TenantsCannotCollide_RealPostgres proves T4's own
// "no tenant can collide with another tenant's idempotency key" against
// the real UNIQUE(tenant_id, operation_id, idempotency_key) constraint.
func TestService_Begin_TenantsCannotCollide_RealPostgres(t *testing.T) {
	db := setupGatewayTestDB(t)
	repo := repository.NewIdempotencyRepository(db)
	svc := idempotency.NewService(repo, time.Hour, "integration-test-owner")

	tenants := repository.NewTenantRepository(db)
	tenantA, tenantB := newTenant(t, tenants).ID, newTenant(t, tenants).ID
	body := []byte(`{"amount":100}`)

	decisionA, err := svc.Begin(context.Background(), tenantA, "b2bCreateTransferV1", "identical-key", body)
	require.NoError(t, err)
	require.Equal(t, idempotency.OutcomeNew, decisionA.Outcome)

	decisionB, err := svc.Begin(context.Background(), tenantB, "b2bCreateTransferV1", "identical-key", body)
	require.NoError(t, err)
	require.Equal(t, idempotency.OutcomeNew, decisionB.Outcome, "a different tenant using the identical key must claim independently, not collide")
	require.NotEqual(t, decisionA.RecordID, decisionB.RecordID)
	require.NotEqual(t, decisionA.DownstreamKey, decisionB.DownstreamKey)
}
