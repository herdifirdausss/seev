package fraud

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/herdifirdausss/seev/internal/fraud/rules"
	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/pkg/messaging"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

// Broker is what the async velocity consumer needs from the message broker
// — topology declaration plus consumption, nothing publish-side.
type Broker interface {
	messaging.Consumer
	messaging.TopologyManager
}

const (
	velocityQueue       = "ledger.events.fraud"
	velocityConsumerTag = "fraud-velocity-consumer"
	velocityTTL         = 2 * time.Hour
)

func (m *Module) Start(ctx context.Context) error {
	m.startSpillFlusher(ctx)
	if m.broker == nil || m.store == nil {
		return nil
	}
	if err := m.broker.DeclareTopology(ctx, messaging.QueueConfig{
		Queue: velocityQueue, RoutingKeys: []string{events.TypeTransactionPosted},
	}); err != nil {
		return fmt.Errorf("fraud: declare topology: %w", err)
	}
	consumeCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	go func() {
		if err := m.broker.Consume(consumeCtx, messaging.ConsumeOptions{
			Queue: velocityQueue, ConsumerTag: velocityConsumerTag,
			PrefetchCount: 10, MaxDeliveryAttempts: 5,
		}, m.handleDelivery); err != nil {
			m.logger.Error("fraud: velocity consumer stopped", "error", err)
		}
	}()
	return nil
}

func (m *Module) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.spillCancel != nil {
		m.spillCancel()
	}
}

func (m *Module) handleDelivery(ctx context.Context, delivery amqp.Delivery) error {
	var event events.TransactionPosted
	if err := json.Unmarshal(delivery.Body, &event); err != nil {
		return fmt.Errorf("fraud: decode TransactionPosted: %w", err)
	}
	if event.UserID == nil {
		return nil
	}
	if delivery.MessageId == "" {
		return fmt.Errorf("fraud: message id is required")
	}
	at := event.OccurredAt
	if at.IsZero() {
		at = time.Now()
	}
	key := rules.VelocityKey(event.UserID.String(), at)
	if err := m.store.Record(ctx, delivery.MessageId, key, velocityTTL); err != nil {
		return fmt.Errorf("fraud: increment velocity: %w", err)
	}
	// request_id here is the CorrelationId the publisher stamped on this
	// message (docs/plan/36 Task T4/T6) — logging it is what lets a trace
	// span the async hop from "HTTP/gRPC request that posted the
	// transaction" to "this velocity counter increment", the same way the
	// synchronous screening call already does via pkg/fraudcheck.
	m.logger.Info("fraud: velocity recorded", "request_id", middleware.RequestIDFromCtx(ctx), "user_id", event.UserID.String())
	return nil
}
