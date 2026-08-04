package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/herdifirdausss/seev/contracts/events/ledger"
	"github.com/herdifirdausss/seev/internal/platform/database/identifiers"
	"github.com/herdifirdausss/seev/internal/platform/messaging"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/model"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/repository"
)

const (
	consumerQueueName = "ledger.events.merchant_webhooks"
	consumerTag       = "merchant-webhook-consumer"
)

// Broker is the subset of messaging.Broker the consumer depends on — a
// local structural interface (mirrors services/gateway/internal/notification's own Broker) so
// unit tests can inject a mock without a real AMQP connection.
type Broker interface {
	messaging.Consumer
	messaging.TopologyManager
}

// Consumer is T7's inbound side — it subscribes to
// events.TypeTransactionPosted, filters to merchant-owned transactions,
// dedupes into an immutable WebhookEvent, and fans out a pending
// WebhookDelivery to every one of the tenant's endpoints subscribed to
// that event type. RelayWorker (relay.go) is the outbound side that
// drains what this consumer enqueues.
type Consumer struct {
	webhooks repository.WebhookRepository
	tenants  repository.TenantRepository
	broker   Broker
	logger   *slog.Logger
	cancel   context.CancelFunc
}

// NewConsumer panics on a nil repository dependency — same construct-now
// posture as every other component in this package.
func NewConsumer(webhooks repository.WebhookRepository, tenants repository.TenantRepository, broker Broker, logger *slog.Logger) *Consumer {
	if webhooks == nil {
		panic("merchant/webhook: NewConsumer requires a non-nil WebhookRepository")
	}
	if tenants == nil {
		panic("merchant/webhook: NewConsumer requires a non-nil TenantRepository")
	}
	if broker == nil {
		panic("merchant/webhook: NewConsumer requires a non-nil Broker")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Consumer{webhooks: webhooks, tenants: tenants, broker: broker, logger: logger}
}

// Start declares the queue topology, then launches the consumer in its own
// goroutine — matches services/gateway/internal/notification.Module.Start's own shape exactly
// (this codebase's one prior RabbitMQ-consumer template).
func (c *Consumer) Start(ctx context.Context) error {
	if err := c.broker.DeclareTopology(ctx, messaging.QueueConfig{
		Queue:       consumerQueueName,
		RoutingKeys: []string{events.TypeTransactionPosted},
	}); err != nil {
		return fmt.Errorf("merchant/webhook: declare topology: %w", err)
	}

	consumeCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	go func() {
		if err := c.broker.Consume(consumeCtx, messaging.ConsumeOptions{
			Queue:               consumerQueueName,
			ConsumerTag:         consumerTag,
			PrefetchCount:       10,
			MaxDeliveryAttempts: 5,
		}, c.handleDelivery); err != nil {
			c.logger.Error("merchant/webhook: consumer stopped", "error", err)
		}
	}()
	return nil
}

// Stop cancels the consumer goroutine. Safe to call even if Start was
// never called or failed — cancel is nil-checked.
func (c *Consumer) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

// handleDelivery is the messaging.HandlerFunc bound to consumerQueueName:
// decode -> filter to merchant-owned -> dedupe -> persist the immutable
// external event -> fan out one pending delivery per subscribed endpoint.
// Every returned error is a plain (non-Retriable) error, same reasoning as
// services/gateway/internal/notification's own handleDelivery: a malformed payload never succeeds
// differently on redelivery, and internal/platform/messaging's own shouldRequeue already
// gives one free retry for transient DB errors without this handler
// needing to distinguish transient from permanent itself.
func (c *Consumer) handleDelivery(ctx context.Context, d amqp.Delivery) error {
	var ev events.TransactionPosted
	if err := json.Unmarshal(d.Body, &ev); err != nil {
		return fmt.Errorf("merchant/webhook: decode TransactionPosted: %w", err)
	}
	if err := ev.Validate(); err != nil {
		return fmt.Errorf("merchant/webhook: validate TransactionPosted: %w", err)
	}

	// Not a merchant-owned transaction (Plan 57 T5/T6's own "nil for every
	// transaction type except merchant_transfer/merchant_payin_credit/
	// merchant_payout_*" convention) — nothing for T7 to relay.
	if ev.MerchantTenantID == nil {
		return nil
	}
	tenantID := *ev.MerchantTenantID

	var sourceEventID uuid.UUID
	if ev.EventID != nil {
		sourceEventID = *ev.EventID
	} else {
		var err error
		sourceEventID, err = uuid.Parse(d.MessageId)
		if err != nil {
			return fmt.Errorf("merchant/webhook: invalid message id %q: %w", d.MessageId, err)
		}
	}

	// Dedup on (tenant, source event, type) — RabbitMQ at-least-once
	// redelivery of an already-processed event is treated identically to
	// "already processed", not an error (T7 acceptance: dedup produces
	// exactly one external event per logical ledger event).
	if _, found, err := c.webhooks.GetEventBySource(ctx, tenantID, sourceEventID, transactionPostedExternalType); err != nil {
		return fmt.Errorf("merchant/webhook: check existing event: %w", err)
	} else if found {
		return nil
	}

	tenant, err := c.tenants.GetByID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("merchant/webhook: get tenant %s: %w", tenantID, err)
	}
	// livemode reflects the tenant's own environment
	// (docs/reference/c1-b2b-design.md §2: "livemode reflects the
	// tenant/key environment") — a tenant is entirely sandbox or entirely
	// live, never mixed (§3.1's own provisioning flow), so every endpoint
	// this fans out to below shares the same livemode value.
	livemode := tenant.Environment == "live"

	publicID := externalEventPublicID(sourceEventID)
	dataJSON, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("merchant/webhook: marshal event data: %w", err)
	}
	body, err := BuildTransactionPostedEnvelope(publicID, livemode, ev.OccurredAt, ev)
	if err != nil {
		return fmt.Errorf("merchant/webhook: build envelope: %w", err)
	}

	whEvent := model.WebhookEvent{
		ID: identifiers.NewV7(), PublicID: publicID, TenantID: tenantID,
		EventType: transactionPostedExternalType, SchemaVersion: 1, Livemode: livemode,
		Payload: dataJSON, PayloadBytes: body, SourceEventID: sourceEventID,
	}
	if err := c.webhooks.CreateEvent(ctx, whEvent); err != nil {
		return fmt.Errorf("merchant/webhook: create webhook event: %w", err)
	}

	endpoints, err := c.webhooks.ListEndpoints(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("merchant/webhook: list endpoints for tenant %s: %w", tenantID, err)
	}
	for _, endpoint := range endpoints {
		if endpoint.Status != "enabled" || !subscribes(endpoint, transactionPostedExternalType) {
			continue
		}
		delivery := model.WebhookDelivery{
			ID: identifiers.NewV7(), PublicID: "whd_" + uuid.NewString()[:16], TenantID: tenantID,
			EndpointID: endpoint.ID, EventID: whEvent.ID, Status: "pending", AttemptCount: 0,
		}
		// CreateDelivery's own UNIQUE(endpoint_id, event_id) dedup (T7
		// acceptance: "one endpoint receives at most one automatic
		// delivery record per event") makes this idempotent even if a
		// prior attempt at this handler got partway through the fan-out
		// loop before a redelivery restarted it.
		if _, err := c.webhooks.CreateDelivery(ctx, delivery); err != nil {
			return fmt.Errorf("merchant/webhook: create delivery for endpoint %s: %w", endpoint.ID, err)
		}
	}
	return nil
}

func subscribes(endpoint model.WebhookEndpoint, eventType string) bool {
	return slices.Contains(endpoint.SubscribedEvents, eventType)
}
