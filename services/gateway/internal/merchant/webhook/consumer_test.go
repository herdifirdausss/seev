package webhook

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/contracts/events/ledger"
	"github.com/herdifirdausss/seev/internal/platform/database/identifiers"
	"github.com/herdifirdausss/seev/internal/platform/messaging"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/model"
)

// newTestConsumer wires an unused messaging.MockBroker — these tests call
// handleDelivery directly rather than going through Start/Consume, so
// DeclareTopology/Consume are never actually invoked.
func newTestConsumer(webhooks *fakeWebhookRepository, tenants *fakeTenantRepository) *Consumer {
	return NewConsumer(webhooks, tenants, &messaging.MockBroker{}, discardLogger())
}

func merchantTransactionPostedDelivery(t *testing.T, tenantID uuid.UUID) amqp.Delivery {
	t.Helper()
	txID := identifiers.NewV7()
	ev := events.NewTransactionPosted(
		txID, "merchant_transfer", "10000", "IDR",
		nil, nil, nil, "", time.Now(),
		nil, nil, "", &tenantID,
	)
	body, err := json.Marshal(ev)
	require.NoError(t, err)
	return amqp.Delivery{Body: body, MessageId: uuid.NewString()}
}

func TestConsumer_FiltersNonMerchantEvents(t *testing.T) {
	webhooks := newFakeWebhookRepository()
	tenants := newFakeTenantRepository()
	c := newTestConsumer(webhooks, tenants)

	ev := events.NewTransactionPosted(
		identifiers.NewV7(), "transfer_p2p", "10000", "IDR",
		nil, nil, nil, "", time.Now(),
		nil, nil, "", nil, // MerchantTenantID nil — not merchant-owned
	)
	body, err := json.Marshal(ev)
	require.NoError(t, err)

	err = c.handleDelivery(context.Background(), amqp.Delivery{Body: body, MessageId: uuid.NewString()})
	require.NoError(t, err)

	assert.Empty(t, webhooks.events, "a non-merchant TransactionPosted must produce zero webhook events")
	assert.Empty(t, webhooks.deliveries)
}

func TestConsumer_DedupesRedelivery(t *testing.T) {
	webhooks := newFakeWebhookRepository()
	tenants := newFakeTenantRepository()
	tenantID := identifiers.NewV7()
	require.NoError(t, tenants.Create(context.Background(), model.Tenant{ID: tenantID, Environment: "live"}))
	c := newTestConsumer(webhooks, tenants)

	delivery := merchantTransactionPostedDelivery(t, tenantID)

	require.NoError(t, c.handleDelivery(context.Background(), delivery))
	require.Len(t, webhooks.events, 1)

	// Redeliver the SAME message (RabbitMQ at-least-once) — must be a
	// silent no-op, not a second event.
	require.NoError(t, c.handleDelivery(context.Background(), delivery))
	assert.Len(t, webhooks.events, 1, "dedup must produce exactly one external event per logical ledger event")
}

func TestConsumer_FansOutOnlyToEnabledSubscribedEndpoints(t *testing.T) {
	webhooks := newFakeWebhookRepository()
	tenants := newFakeTenantRepository()
	tenantID := identifiers.NewV7()
	require.NoError(t, tenants.Create(context.Background(), model.Tenant{ID: tenantID, Environment: "live"}))
	c := newTestConsumer(webhooks, tenants)

	subscribed := model.WebhookEndpoint{
		ID: identifiers.NewV7(), PublicID: "wh_a", TenantID: tenantID, URL: "https://a.example/hook",
		Status: "enabled", SubscribedEvents: []string{transactionPostedExternalType}, Environment: "live",
	}
	notSubscribed := model.WebhookEndpoint{
		ID: identifiers.NewV7(), PublicID: "wh_b", TenantID: tenantID, URL: "https://b.example/hook",
		Status: "enabled", SubscribedEvents: []string{"other.event.v1"}, Environment: "live",
	}
	disabled := model.WebhookEndpoint{
		ID: identifiers.NewV7(), PublicID: "wh_c", TenantID: tenantID, URL: "https://c.example/hook",
		Status: "disabled", SubscribedEvents: []string{transactionPostedExternalType}, Environment: "live",
	}
	for _, e := range []model.WebhookEndpoint{subscribed, notSubscribed, disabled} {
		require.NoError(t, webhooks.CreateEndpoint(context.Background(), e))
	}

	require.NoError(t, c.handleDelivery(context.Background(), merchantTransactionPostedDelivery(t, tenantID)))

	require.Len(t, webhooks.deliveries, 1, "only the enabled, subscribed endpoint should receive a delivery")
	for _, d := range webhooks.deliveries {
		assert.Equal(t, subscribed.ID, d.EndpointID)
		assert.Equal(t, "pending", d.Status)
	}
}

func TestConsumer_LivemodeReflectsTenantEnvironment(t *testing.T) {
	webhooks := newFakeWebhookRepository()
	tenants := newFakeTenantRepository()
	tenantID := identifiers.NewV7()
	require.NoError(t, tenants.Create(context.Background(), model.Tenant{ID: tenantID, Environment: "sandbox"}))
	c := newTestConsumer(webhooks, tenants)

	require.NoError(t, c.handleDelivery(context.Background(), merchantTransactionPostedDelivery(t, tenantID)))

	require.Len(t, webhooks.events, 1)
	for _, e := range webhooks.events {
		assert.False(t, e.Livemode, "a sandbox-environment tenant's event must have livemode=false")
	}
}
