//go:build integration

// Package webhook_test proves Plan 57 T7's outbound webhook relay against a
// real PostgreSQL and a real HTTP server — the standing project convention
// of verifying task-by-task work live, not only against hand-written
// fakes.
package webhook_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/internal/merchant/webhook"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/cryptox"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/messaging"
)

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations")
}

// setupGatewayTestDB mirrors internal/merchant/repository's own
// integration-test helper of the same name: one Postgres container holding
// seev_ledger (for the cluster-wide app_service/app_readonly roles every
// gateway migration's GRANT depends on) and a separate seev_gateway
// database with T7's own webhook tables.
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

func testRing(t *testing.T) *cryptox.Ring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 11)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	return ring
}

func merchantEventDelivery(t *testing.T, tenantID uuid.UUID) amqp.Delivery {
	t.Helper()
	ev := events.NewTransactionPosted(
		generalutil.NewV7(), "merchant_transfer", "50000", "IDR",
		nil, nil, nil, "", time.Now(),
		nil, nil, "", &tenantID,
	)
	body, err := json.Marshal(ev)
	require.NoError(t, err)
	return amqp.Delivery{Body: body, MessageId: uuid.NewString()}
}

// TestWebhookRelay_EndToEnd proves, against real Postgres and a real HTTP
// receiver, the core T7 acceptance chain: consumer dedup -> one endpoint
// gets at most one automatic delivery -> relay dispatch delivers with a
// verifiable signature -> the stored secret is ciphertext, never plaintext
// -> replay creates a new delivery id sharing the original event id -> a
// worker restart (simulated: a second ClaimDue call after the first
// delivery's lease has already been marked delivered, plus a manually
// re-queued delivery with an EXPIRED lease) recovers an abandoned lease.
func TestWebhookRelay_EndToEnd(t *testing.T) {
	db := setupGatewayTestDB(t)
	ring := testRing(t)
	ctx := context.Background()

	tenants := repository.NewTenantRepository(db)
	webhooks := repository.NewWebhookRepository(db)

	tenantID := generalutil.NewV7()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantID, PublicID: "tn_test", ExternalCode: "T7E2E", Name: "T7 E2E Tenant",
		Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))

	var receivedSig, receivedBody string
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		receivedSig = r.Header.Get(webhook.SignatureHeader)
		buf, _ := io.ReadAll(r.Body)
		receivedBody = string(buf)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := webhook.NewService(webhooks, ring)
	// environment "sandbox" — httptest.Server binds to loopback, and the
	// relay's own SSRF guard (ssrf.go) correctly refuses to dial loopback
	// in "live" mode; sandbox mode is what legitimately permits a local
	// receiver (docs/reference/c1-b2b-design.md §4). The live-vs-sandbox
	// SSRF distinction itself is covered separately by
	// TestSafeClient_LiveModeRejectsLoopback / TestRelayWorker_LiveModeSSRFRejectionIsRetried.
	endpoint, secret, err := svc.CreateEndpoint(ctx, tenantID, server.URL, "sandbox", []string{"transaction.posted.v1"}, nil)
	require.NoError(t, err)

	// The stored ciphertext must never equal or contain the plaintext
	// secret (T7 acceptance: "the secret ciphertext, never plaintext, is
	// what's actually stored") — check the raw DB row, not just the Go
	// struct.
	var rawCiphertext []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT secret_ciphertext FROM merchant_webhook_endpoints WHERE id = $1`, endpoint.ID).Scan(&rawCiphertext))
	require.NotContains(t, string(rawCiphertext), secret)

	handleFn := capturedConsumerHandler(t, webhooks, tenants)
	delivery := merchantEventDelivery(t, tenantID)

	// Dedup: redeliver the SAME message twice — exactly one WebhookEvent
	// and exactly one delivery for this (endpoint, event) pair must exist.
	require.NoError(t, handleFn(ctx, delivery))
	require.NoError(t, handleFn(ctx, delivery))

	var eventCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM merchant_webhook_events WHERE tenant_id = $1`, tenantID).Scan(&eventCount))
	require.Equal(t, 1, eventCount, "dedup must produce exactly one external event per logical ledger event")

	deliveries, err := webhooks.ListDeliveries(ctx, tenantID, 10)
	require.NoError(t, err)
	require.Len(t, deliveries, 1, "one endpoint must receive at most one automatic delivery record per event")

	relay := webhook.NewRelayWorker(webhooks, ring, nil)
	processed, failed, err := relay.ProcessOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Equal(t, 0, failed)
	require.Equal(t, int32(1), atomic.LoadInt32(&hits))

	delivered, err := webhooks.GetDelivery(ctx, tenantID, deliveries[0].ID)
	require.NoError(t, err)
	require.Equal(t, "delivered", delivered.Status)

	expectedSig := webhook.Sign([]byte(secret), delivered.CreatedAt.Unix(), func() []byte {
		ev, err := webhooks.GetEventByID(ctx, delivered.EventID)
		require.NoError(t, err)
		return ev.PayloadBytes
	}())
	require.Equal(t, expectedSig, receivedSig)

	ev, err := webhooks.GetEventByID(ctx, delivered.EventID)
	require.NoError(t, err)
	require.Equal(t, string(ev.PayloadBytes), receivedBody)

	// Replay: new delivery id, same event id, immediately due again.
	replay, err := svc.Replay(ctx, tenantID, delivered.ID)
	require.NoError(t, err)
	require.NotEqual(t, delivered.ID, replay.ID)
	require.Equal(t, delivered.EventID, replay.EventID)

	processed2, _, err := relay.ProcessOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, processed2, "the replay delivery must be picked up and dispatched on the next poll")
	require.Equal(t, int32(2), atomic.LoadInt32(&hits))

	// Worker restart recovers an expired lease: manually put a THIRD
	// delivery into 'pending' with a lease that already expired (simulates
	// a worker that claimed it, then crashed before finishing).
	staleDelivery := model.WebhookDelivery{
		ID: generalutil.NewV7(), PublicID: "whd_stale", TenantID: tenantID,
		EndpointID: endpoint.ID, EventID: delivered.EventID, ReplayOfDeliveryID: &replay.ID,
		Status: "pending",
	}
	require.NoError(t, webhooks.CreateReplayDelivery(ctx, staleDelivery))
	expiredOwner := "dead-worker-instance"
	expiredLease := time.Now().Add(-1 * time.Hour)
	_, err = db.ExecContext(ctx, `UPDATE merchant_webhook_deliveries SET lease_owner = $1, lease_expires_at = $2 WHERE id = $3`,
		expiredOwner, expiredLease, staleDelivery.ID)
	require.NoError(t, err)

	processed3, _, err := relay.ProcessOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, processed3, "ClaimDue must reclaim a delivery whose lease already expired")
	require.Equal(t, int32(3), atomic.LoadInt32(&hits))
}

// capturedConsumerHandler exercises Consumer the same way production code
// would — Start() declares topology and registers a handler via
// Consume() — but against a MockBroker whose ConsumeFn just captures the
// handler instead of opening a real AMQP channel, so this black-box test
// can invoke the exact function Start wired up, synchronously, with no
// goroutine/channel dance needed to observe results.
func capturedConsumerHandler(t *testing.T, webhooks repository.WebhookRepository, tenants repository.TenantRepository) messaging.HandlerFunc {
	t.Helper()
	// Start() calls broker.Consume in its own goroutine (matches
	// internal/notify.Module.Start's own async shape) — capturedCh
	// synchronizes this test with that goroutine instead of racing on a
	// plain variable.
	capturedCh := make(chan messaging.HandlerFunc, 1)
	broker := &messaging.MockBroker{
		DeclareTopologyFn: func(context.Context, messaging.QueueConfig) error { return nil },
		ConsumeFn: func(_ context.Context, _ messaging.ConsumeOptions, h messaging.HandlerFunc) error {
			capturedCh <- h
			return nil
		},
	}
	consumer := webhook.NewConsumer(webhooks, tenants, broker, nil)
	require.NoError(t, consumer.Start(context.Background()))

	select {
	case h := <-capturedCh:
		return h
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not register a handler via Consume within 5s")
		return nil
	}
}
