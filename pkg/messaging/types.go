package messaging

// types.go — public interfaces, errors, and HandlerFunc.
//
// Dependency rule for application code:
//
//	- Services that only publish  → depend on Publisher
//	- Services that only consume  → depend on Consumer
//	- Services that declare queues → depend on TopologyManager
//	- Entry-points / health probes → depend on Broker (embeds all three)
//
// Never import *RabbitMQ directly in application code — use these interfaces.
// This enables trivial mock injection in unit tests.

import (
	"context"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ─── Handler ─────────────────────────────────────────────────────────────────

// HandlerFunc processes a single delivered AMQP message.
//
// Contract:
//
//	nil return          → d.Ack(false)            — processed successfully
//	Retriable(err)      → d.Nack(false, requeue)  — requeue if first delivery
//	any other error     → d.Nack(false, false)     — route to DLQ
//	panic               → recovered; d.Nack → DLQ; consumer continues
type HandlerFunc func(ctx context.Context, d amqp.Delivery) error

// ─── Interfaces ───────────────────────────────────────────────────────────────

// Publisher publishes messages to a RabbitMQ exchange.
// Safe for concurrent use across goroutines and modules.
type Publisher interface {
	// Publish marshals payload as JSON and publishes to the broker's
	// DefaultExchange using routingKey. An idempotency MessageID is
	// auto-generated (UUID v4).
	Publish(ctx context.Context, routingKey string, payload any) error

	// PublishTo is like Publish but accepts explicit PublishOptions for
	// per-message exchange, routing-key, or idempotency-key overrides.
	PublishTo(ctx context.Context, opts PublishOptions, payload any) error
}

// Consumer subscribes to messages from an arbitrary queue.
// Queue must already exist (declared via TopologyManager.DeclareTopology).
type Consumer interface {
	// Consume blocks until ctx is cancelled, automatically recovering from
	// channel and connection failures. Each call should run in its own goroutine.
	Consume(ctx context.Context, opts ConsumeOptions, handler HandlerFunc) error
}

// TopologyManager declares AMQP topology (exchanges, queues, bindings, DLX).
// Safe to call concurrently from multiple modules at startup.
type TopologyManager interface {
	// DeclareTopology idempotently creates the exchange, queue, DLX/DLQ, and
	// all bindings described by cfg. Safe to call on reconnect or at startup.
	DeclareTopology(ctx context.Context, cfg QueueConfig) error
}

// Broker is the full messaging contract used by service entry-points and
// health probes. Application modules should depend on the narrower interfaces
// (Publisher, Consumer, TopologyManager) rather than Broker directly.
type Broker interface {
	Publisher
	Consumer
	TopologyManager

	// HealthCheck returns nil if the AMQP connection is live.
	// Suitable for Kubernetes readiness/liveness probes.
	HealthCheck() error

	// Close drains in-flight handlers then shuts the connection gracefully.
	// Safe to call multiple times.
	Close() error
}

// ─── Options ─────────────────────────────────────────────────────────────────

// PublishOptions configures a single Publish call.
// Zero-value fields fall back to broker defaults.
type PublishOptions struct {
	// Exchange to publish to. Empty string → BrokerConfig.DefaultExchange.
	Exchange string

	// RoutingKey is required.
	RoutingKey string

	// MessageID is the idempotency key. Empty string → auto-generated UUID v4.
	// For fintech workloads, supply your transaction/order ID to enable
	// exactly-once detection at the consumer side.
	MessageID string
}

func (o PublishOptions) validate() error {
	if o.RoutingKey == "" {
		return fmt.Errorf("PublishOptions: RoutingKey is required")
	}
	return nil
}

// ConsumeOptions configures a single Consume call.
// Each call to Consume should have a unique ConsumerTag within the process.
type ConsumeOptions struct {
	// Queue is the AMQP queue to consume from. Must already be declared via
	// DeclareTopology. Required.
	Queue string

	// ConsumerTag is the AMQP consumer identifier. Should be human-readable
	// and unique per process (e.g., "payment-processor", "notification-sender").
	// A stable UUID suffix is appended internally to prevent RESOURCE_LOCKED.
	// Required.
	ConsumerTag string

	// PrefetchCount is the maximum number of unacknowledged messages the broker
	// will deliver to this consumer. Must be > 0. Default: 10.
	//
	// WARNING: 0 means unlimited prefetch — the broker will flood the consumer.
	// Always set an explicit value appropriate to your handler throughput.
	PrefetchCount int

	// MaxDeliveryAttempts is how many times a message may be delivered (across
	// all death cycles) before it is routed to the DLQ without requeue.
	// Default: 5. Must be > 0.
	MaxDeliveryAttempts int
}

func (o ConsumeOptions) validate() error {
	if o.Queue == "" {
		return fmt.Errorf("ConsumeOptions: Queue is required")
	}
	if o.ConsumerTag == "" {
		return fmt.Errorf("ConsumeOptions: ConsumerTag is required")
	}
	return nil
}

func (o *ConsumeOptions) applyDefaults() {
	if o.PrefetchCount <= 0 {
		o.PrefetchCount = 10
	}
	if o.MaxDeliveryAttempts <= 0 {
		o.MaxDeliveryAttempts = 5
	}
}

// ─── Internal abstractions ────────────────────────────────────────────────────

// amqpConnection abstracts *amqp.Connection for unit-test injection.
type amqpConnection interface {
	Channel() (*amqp.Channel, error)
	IsClosed() bool
	Close() error
	NotifyClose(chan *amqp.Error) chan *amqp.Error
}

// dialFn establishes a new AMQP connection. ctx is forwarded to the TCP dialer
// so reconnect attempts can be cancelled during shutdown.
type dialFn func(ctx context.Context, cfg *BrokerConfig) (amqpConnection, error)

// ─── Sentinel Errors ─────────────────────────────────────────────────────────

// All sentinel errors implement the standard errors.Is interface.
// Callers should always use errors.Is/As, never string comparison.
var (
	// ErrNotConnected is returned when the AMQP connection is unavailable.
	ErrNotConnected = errors.New("rabbitmq: not connected")

	// ErrBrokerNacked is returned when the broker explicitly nacks a publish.
	ErrBrokerNacked = errors.New("rabbitmq: broker nacked message")

	// ErrPublishTimeout is returned when the publisher confirm is not received
	// within BrokerConfig.PublishTimeout.
	ErrPublishTimeout = errors.New("rabbitmq: publish confirm timeout")

	// ErrConfirmChannelClosed is returned when the confirms channel closes
	// unexpectedly (e.g., mid-publish connection loss).
	ErrConfirmChannelClosed = errors.New("rabbitmq: confirms channel closed unexpectedly")

	// ErrMaxReconnectExceeded is returned when all reconnect attempts fail.
	ErrMaxReconnectExceeded = errors.New("rabbitmq: max reconnect attempts exceeded")

	// ErrInvalidConfig is returned when config validation fails.
	ErrInvalidConfig = errors.New("rabbitmq: invalid configuration")

	// ErrInvalidTopology is returned when QueueConfig validation fails.
	ErrInvalidTopology = errors.New("rabbitmq: invalid topology config")

	// ErrInvalidConsumeOptions is returned when ConsumeOptions validation fails.
	ErrInvalidConsumeOptions = errors.New("rabbitmq: invalid consume options")
)

// ─── PublishError ─────────────────────────────────────────────────────────────

// PublishError is the structured error returned by all Publish operations.
// Use errors.Is/As to inspect the wrapped Cause sentinel.
type PublishError struct {
	Exchange   string
	RoutingKey string
	MessageID  string
	Cause      error
}

func (e *PublishError) Error() string {
	return fmt.Sprintf(
		"rabbitmq publish [exchange=%s routing_key=%s message_id=%s]: %v",
		e.Exchange, e.RoutingKey, e.MessageID, e.Cause,
	)
}

func (e *PublishError) Unwrap() error { return e.Cause }

// Is allows errors.Is(err, ErrBrokerNacked) to traverse PublishError.
func (e *PublishError) Is(target error) bool {
	return errors.Is(e.Cause, target)
}

// ─── RetriableError ───────────────────────────────────────────────────────────

// RetriableError signals that a handler failure is transient and the message
// should be requeued for a subsequent delivery attempt.
//
// The broker honours MaxDeliveryAttempts before routing to DLQ.
//
// Usage:
//
//	func myHandler(ctx context.Context, d amqp.Delivery) error {
//	    if err := callUpstream(ctx); err != nil {
//	        return messaging.Retriable(fmt.Errorf("upstream unavailable: %w", err))
//	    }
//	    return nil
//	}
type RetriableError struct{ Err error }

func (e *RetriableError) Error() string { return e.Err.Error() }
func (e *RetriableError) Unwrap() error { return e.Err }

// Retriable wraps err as a RetriableError.
func Retriable(err error) error { return &RetriableError{Err: err} }

// IsRetriable reports whether err (or any cause in its chain) is a RetriableError.
func IsRetriable(err error) bool {
	var r *RetriableError
	return errors.As(err, &r)
}
