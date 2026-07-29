//go:build integration

// Race tests for T10b's §23.8 items 3-5, run against real Postgres with
// genuine goroutine concurrency (not the sequential "simulated restart"
// pattern webhook_integration_test.go itself already documents as a gap).
// Run with -race to get memory-safety coverage on top of the business
// assertions.
package webhook_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/internal/merchant/webhook"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// TestRelayWorker_ConcurrentWorkers_ClaimDueIsExclusive proves §23.8 item 3:
// two RelayWorker instances calling ProcessOnce AT THE SAME TIME against
// the same due delivery must never both dispatch it — ClaimDue's
// `FOR UPDATE SKIP LOCKED` (relay.go's own doc comment) is what's supposed
// to make this safe; this proves it under genuine goroutine contention,
// not the sequential two-calls-in-a-row pattern
// TestWebhookRelay_EndToEnd's "worker restart" case already covers.
func TestRelayWorker_ConcurrentWorkers_ClaimDueIsExclusive(t *testing.T) {
	db := setupGatewayTestDB(t)
	ring := testRing(t)
	ctx := context.Background()

	tenants := repository.NewTenantRepository(db)
	webhooks := repository.NewWebhookRepository(db)

	tenantID := generalutil.NewV7()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantID, PublicID: "tn_race3", ExternalCode: "T10B-RACE3", Name: "Race Test Tenant",
		Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := webhook.NewService(webhooks, ring)
	_, _, err := svc.CreateEndpoint(ctx, tenantID, server.URL, "sandbox", []string{"transaction.posted.v1"}, nil)
	require.NoError(t, err)

	handleFn := capturedConsumerHandler(t, webhooks, tenants)
	require.NoError(t, handleFn(ctx, merchantEventDelivery(t, tenantID)))

	deliveries, err := webhooks.ListDeliveries(ctx, tenantID, 10)
	require.NoError(t, err)
	require.Len(t, deliveries, 1, "exactly one due delivery must exist before the concurrent claim")

	// Two independent worker instances, same repo/ring — instanceID differs
	// (NewRelayWorker's own hostname+random-suffix construction), matching
	// how two real replica processes would look to ClaimDue.
	workerA := webhook.NewRelayWorker(webhooks, ring, nil)
	workerB := webhook.NewRelayWorker(webhooks, ring, nil)

	var wg sync.WaitGroup
	var processedA, processedB int
	wg.Add(2)
	go func() { defer wg.Done(); processedA, _, err = workerA.ProcessOnce(ctx); require.NoError(t, err) }()
	go func() { defer wg.Done(); processedB, _, err = workerB.ProcessOnce(ctx); require.NoError(t, err) }()
	wg.Wait()

	assert.Equal(t, 1, processedA+processedB, "exactly one worker must have claimed the single due delivery")
	assert.Equal(t, int32(1), atomic.LoadInt32(&hits), "the receiver must be hit exactly once — no double-dispatch")

	delivered, err := webhooks.GetDelivery(ctx, tenantID, deliveries[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "delivered", delivered.Status)
}

// TestService_Replay_ConcurrentReplaysAllSucceedIndependently proves
// §23.8 item 4: Replay has no uniqueness constraint against its own
// original (the schema's partial unique index on (endpoint_id, event_id)
// explicitly exempts replay rows — migrations/gateway/000004, "every
// replay row ... is exempt") — so N concurrent Replay calls for the SAME
// original delivery must all succeed independently, each producing its
// own distinct delivery row, with no torn state under real concurrent
// writes to the shared original row.
func TestService_Replay_ConcurrentReplaysAllSucceedIndependently(t *testing.T) {
	db := setupGatewayTestDB(t)
	ring := testRing(t)
	ctx := context.Background()

	tenants := repository.NewTenantRepository(db)
	webhooks := repository.NewWebhookRepository(db)

	tenantID := generalutil.NewV7()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantID, PublicID: "tn_race4", ExternalCode: "T10B-RACE4", Name: "Race Test Tenant",
		Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := webhook.NewService(webhooks, ring)
	_, _, err := svc.CreateEndpoint(ctx, tenantID, server.URL, "sandbox", []string{"transaction.posted.v1"}, nil)
	require.NoError(t, err)

	handleFn := capturedConsumerHandler(t, webhooks, tenants)
	require.NoError(t, handleFn(ctx, merchantEventDelivery(t, tenantID)))

	deliveries, err := webhooks.ListDeliveries(ctx, tenantID, 10)
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	originalID := deliveries[0].ID

	const concurrency = 10
	var wg sync.WaitGroup
	results := make([]uuid.UUID, concurrency)
	errs := make([]error, concurrency)
	wg.Add(concurrency)
	for i := range concurrency {
		go func(i int) {
			defer wg.Done()
			replay, replayErr := svc.Replay(ctx, tenantID, originalID)
			results[i] = replay.ID
			errs[i] = replayErr
		}(i)
	}
	wg.Wait()

	seen := make(map[uuid.UUID]bool, concurrency)
	for i, err := range errs {
		require.NoError(t, err, "replay %d must succeed", i)
		require.NotEqual(t, uuid.Nil, results[i])
		assert.False(t, seen[results[i]], "replay %d produced a duplicate delivery id", i)
		seen[results[i]] = true
	}
	assert.Len(t, seen, concurrency, "every concurrent replay must produce its own distinct delivery")

	all, err := webhooks.ListDeliveries(ctx, tenantID, 100)
	require.NoError(t, err)
	assert.Len(t, all, concurrency+1, "original plus every concurrent replay must be stored")
}

// TestRelayWorker_ConcurrentEndpointDisable_NoDeliveryEscapesAfterDisable
// proves §23.8 item 5: processDelivery re-fetches the endpoint fresh
// immediately before dispatch and refuses to send to a non-"enabled"
// endpoint (relay.go) — this drives that check under genuine concurrency
// (draining a batch of due deliveries while another goroutine disables
// the endpoint mid-batch) and proves the invariant that actually matters:
// every delivery ends up either genuinely delivered (and hit the
// receiver) or dead (and did NOT hit the receiver) — never a mismatch
// between recorded status and real HTTP delivery, and never a delivery
// left stuck.
func TestRelayWorker_ConcurrentEndpointDisable_NoDeliveryEscapesAfterDisable(t *testing.T) {
	db := setupGatewayTestDB(t)
	ring := testRing(t)
	ctx := context.Background()

	tenants := repository.NewTenantRepository(db)
	webhooks := repository.NewWebhookRepository(db)

	tenantID := generalutil.NewV7()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantID, PublicID: "tn_race5", ExternalCode: "T10B-RACE5", Name: "Race Test Tenant",
		Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := webhook.NewService(webhooks, ring)
	endpoint, _, err := svc.CreateEndpoint(ctx, tenantID, server.URL, "sandbox", []string{"transaction.posted.v1"}, nil)
	require.NoError(t, err)

	const deliveryCount = 20
	handleFn := capturedConsumerHandler(t, webhooks, tenants)
	for range deliveryCount {
		require.NoError(t, handleFn(ctx, merchantEventDelivery(t, tenantID)))
	}
	deliveries, err := webhooks.ListDeliveries(ctx, tenantID, deliveryCount+5)
	require.NoError(t, err)
	require.Len(t, deliveries, deliveryCount)

	worker := webhook.NewRelayWorker(webhooks, ring, nil)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		// Drain in a loop — a single ProcessOnce call could claim
		// everything before the disable goroutine even starts, which
		// would prove nothing; looping with a small batch keeps both
		// goroutines genuinely racing across the whole batch.
		for range deliveryCount {
			_, _, err := worker.ProcessOnce(ctx)
			require.NoError(t, err)
		}
	}()
	go func() {
		defer wg.Done()
		require.NoError(t, svc.SetEndpointStatus(ctx, tenantID, endpoint.ID, "disabled"))
	}()
	wg.Wait()

	// One more drain pass in case the disable landed after ProcessOnce's
	// last claim left a delivery re-queued (MarkDead is still a terminal
	// write, so nothing should be left claimable, but this closes out any
	// stray lease deterministically before asserting final state).
	_, _, err = worker.ProcessOnce(ctx)
	require.NoError(t, err)

	final, err := webhooks.ListDeliveries(ctx, tenantID, deliveryCount+5)
	require.NoError(t, err)
	require.Len(t, final, deliveryCount)

	var delivered, dead int
	for _, d := range final {
		switch d.Status {
		case "delivered":
			delivered++
		case "dead":
			dead++
		default:
			t.Errorf("delivery %s left in unexpected terminal status %q", d.ID, d.Status)
		}
	}
	assert.Equal(t, deliveryCount, delivered+dead, "every delivery must reach a terminal status")
	assert.Equal(t, int32(delivered), atomic.LoadInt32(&hits), "the receiver hit count must exactly match the delivered count — no delivery marked delivered without a real HTTP hit, and no hit unaccounted for")
}
