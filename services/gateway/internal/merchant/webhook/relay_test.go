package webhook

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/database/identifiers"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/model"
)

func testRing(t *testing.T) *cryptox.Ring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 7)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	return ring
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 100}))
}

// seedEndpointAndEvent creates a tenant-owned endpoint (sealed secret) and
// one immutable webhook event, and returns the plaintext secret alongside
// both, for a relay test to build its own delivery row against.
func seedEndpointAndEvent(t *testing.T, repo *fakeWebhookRepository, ring *cryptox.Ring, tenantID uuid.UUID, targetURL, environment string) (model.WebhookEndpoint, model.WebhookEvent, string) {
	t.Helper()
	secret := "plaintext-test-secret"
	endpointID := identifiers.NewV7()
	ciphertext, err := ring.Seal(secretAAD(endpointID), []byte(secret))
	require.NoError(t, err)

	endpoint := model.WebhookEndpoint{
		ID: endpointID, PublicID: "wh_test", TenantID: tenantID, URL: targetURL, Status: "enabled",
		SecretCiphertext: ciphertext, SecretVersion: ring.CurrentVersion(),
		SubscribedEvents: []string{transactionPostedExternalType}, Environment: environment,
	}
	require.NoError(t, repo.CreateEndpoint(context.Background(), endpoint))

	body, err := BuildTransactionPostedEnvelope("evt_test", true, time.Now(), map[string]string{"amount": "100"})
	require.NoError(t, err)
	event := model.WebhookEvent{
		ID: identifiers.NewV7(), PublicID: "evt_test", TenantID: tenantID, EventType: transactionPostedExternalType,
		SchemaVersion: 1, Livemode: true, PayloadBytes: body, SourceEventID: identifiers.NewV7(),
	}
	require.NoError(t, repo.CreateEvent(context.Background(), event))
	return endpoint, event, secret
}

func TestRelayWorker_SuccessfulDeliveryMarksDelivered(t *testing.T) {
	var gotSig, gotBody string
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		gotSig = r.Header.Get(SignatureHeader)
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	repo := newFakeWebhookRepository()
	ring := testRing(t)
	tenantID := identifiers.NewV7()
	endpoint, event, secret := seedEndpointAndEvent(t, repo, ring, tenantID, server.URL, "sandbox")

	delivery := model.WebhookDelivery{
		ID: identifiers.NewV7(), PublicID: "whd_test", TenantID: tenantID,
		EndpointID: endpoint.ID, EventID: event.ID, Status: "pending",
	}
	created, err := repo.CreateDelivery(context.Background(), delivery)
	require.NoError(t, err)
	require.True(t, created)
	// CreateDelivery stamps CreatedAt server-side — refetch to get the
	// actual value the relay worker will sign with.
	delivery, err = repo.GetDelivery(context.Background(), tenantID, delivery.ID)
	require.NoError(t, err)

	worker := NewRelayWorker(repo, ring, discardLogger())
	processed, failed, err := worker.ProcessOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, 0, failed)
	assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))

	stored, err := repo.GetDelivery(context.Background(), tenantID, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, "delivered", stored.Status)
	require.NotNil(t, stored.LastHTTPStatus)
	assert.Equal(t, http.StatusOK, *stored.LastHTTPStatus)

	expectedSig := Sign([]byte(secret), delivery.CreatedAt.Unix(), event.PayloadBytes)
	assert.Equal(t, expectedSig, gotSig, "the dispatched signature must match Sign(secret, delivery.CreatedAt, payload)")
	assert.Equal(t, string(event.PayloadBytes), gotBody, "retries must resend the exact same bytes")
}

func TestRelayWorker_FailedAttemptSchedulesRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	repo := newFakeWebhookRepository()
	ring := testRing(t)
	tenantID := identifiers.NewV7()
	endpoint, event, _ := seedEndpointAndEvent(t, repo, ring, tenantID, server.URL, "sandbox")

	delivery := model.WebhookDelivery{
		ID: identifiers.NewV7(), PublicID: "whd_test", TenantID: tenantID,
		EndpointID: endpoint.ID, EventID: event.ID, Status: "pending",
	}
	_, err := repo.CreateDelivery(context.Background(), delivery)
	require.NoError(t, err)

	worker := NewRelayWorker(repo, ring, discardLogger())
	processed, failed, err := worker.ProcessOnce(context.Background())
	require.NoError(t, err)
	// processed counts "bookkeeping succeeded" (relay.go's own doc
	// comment): recording a failed HTTP attempt is itself a successful
	// bookkeeping outcome, so it counts as processed, not failed — failed
	// here means the DB write ITSELF errored.
	assert.Equal(t, 1, processed)
	assert.Equal(t, 0, failed)

	stored, err := repo.GetDelivery(context.Background(), tenantID, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", stored.Status)
	assert.Equal(t, 1, stored.AttemptCount)
	require.NotNil(t, stored.NextAttemptAt)
	assert.True(t, stored.NextAttemptAt.After(time.Now()), "a failed attempt must schedule a future retry, not retry immediately")
	require.NotNil(t, stored.LastErrorCode)
	assert.Equal(t, "http_500", *stored.LastErrorCode)
}

func TestRelayWorker_ExhaustedRetriesGoDead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	repo := newFakeWebhookRepository()
	ring := testRing(t)
	tenantID := identifiers.NewV7()
	endpoint, event, _ := seedEndpointAndEvent(t, repo, ring, tenantID, server.URL, "sandbox")

	delivery := model.WebhookDelivery{
		ID: identifiers.NewV7(), PublicID: "whd_test", TenantID: tenantID,
		EndpointID: endpoint.ID, EventID: event.ID, Status: "pending",
		AttemptCount: maxDeliveryAttempts - 1, // this attempt will be the last one allowed
	}
	repo.mu.Lock()
	repo.deliveries[delivery.ID] = delivery
	repo.mu.Unlock()

	worker := NewRelayWorker(repo, ring, discardLogger())
	processed, failed, err := worker.ProcessOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, 0, failed)

	stored, err := repo.GetDelivery(context.Background(), tenantID, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, "dead", stored.Status, "attemptNumber reaching maxDeliveryAttempts must dead-letter, not schedule another retry")
	assert.NotNil(t, stored.DeadAt)
}

func TestRelayWorker_410AutoDisablesEndpointAndKillsDelivery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer server.Close()

	repo := newFakeWebhookRepository()
	ring := testRing(t)
	tenantID := identifiers.NewV7()
	endpoint, event, _ := seedEndpointAndEvent(t, repo, ring, tenantID, server.URL, "sandbox")

	delivery := model.WebhookDelivery{
		ID: identifiers.NewV7(), PublicID: "whd_test", TenantID: tenantID,
		EndpointID: endpoint.ID, EventID: event.ID, Status: "pending",
	}
	_, err := repo.CreateDelivery(context.Background(), delivery)
	require.NoError(t, err)

	worker := NewRelayWorker(repo, ring, discardLogger())
	_, _, err = worker.ProcessOnce(context.Background())
	require.NoError(t, err)

	storedDelivery, err := repo.GetDelivery(context.Background(), tenantID, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, "dead", storedDelivery.Status)

	storedEndpoint, err := repo.GetEndpoint(context.Background(), tenantID, endpoint.ID)
	require.NoError(t, err)
	assert.Equal(t, "disabled", storedEndpoint.Status, "a 410 must auto-disable the endpoint (TM-16 / T7 acceptance)")
}

func TestRelayWorker_LiveModeSSRFRejectionIsRetried(t *testing.T) {
	repo := newFakeWebhookRepository()
	ring := testRing(t)
	tenantID := identifiers.NewV7()
	// A loopback URL in "live" mode must be refused by the SSRF guard —
	// dispatch() returns a transport error, not an HTTP status.
	endpoint, event, _ := seedEndpointAndEvent(t, repo, ring, tenantID, "http://127.0.0.1:1/webhook", "live")

	delivery := model.WebhookDelivery{
		ID: identifiers.NewV7(), PublicID: "whd_test", TenantID: tenantID,
		EndpointID: endpoint.ID, EventID: event.ID, Status: "pending",
	}
	_, err := repo.CreateDelivery(context.Background(), delivery)
	require.NoError(t, err)

	worker := NewRelayWorker(repo, ring, discardLogger())
	processed, failed, err := worker.ProcessOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, 0, failed)

	stored, err := repo.GetDelivery(context.Background(), tenantID, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", stored.Status)
	require.NotNil(t, stored.LastErrorCode)
	assert.Equal(t, "dispatch_error", *stored.LastErrorCode)
}

func TestNextAttemptAt_GrowsAndCaps(t *testing.T) {
	for attempt := 1; attempt <= 12; attempt++ {
		at := nextAttemptAt(attempt)
		delay := time.Until(at)
		maxPossible := time.Duration(float64(backoffCapSeconds)*1.5) * time.Second
		assert.LessOrEqual(t, delay, maxPossible+time.Second, "attempt %d delay must respect the 15m cap plus jitter", attempt)
	}
}
