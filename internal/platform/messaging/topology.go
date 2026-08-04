package messaging

// topology.go — QueueConfig and DeclareTopology.
//
// Each module calls DeclareTopology at startup with its own QueueConfig.
// The operation is idempotent: safe to call on every reconnect and from
// multiple goroutines concurrently (each call uses an isolated channel).
//
// Topology model per queue:
//
//	DefaultExchange (topic) ──routing-key──▶ Queue ──x-dead-letter──▶ DLX (topic) ──#──▶ DLQ
//
// DLQ wildcard binding (#) catches all dead-lettered messages regardless of
// their original routing key — necessary for DLQ inspection and replay.

import (
	"context"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// QueueConfig describes a single queue, its exchange binding(s), and its
// dead-letter topology. Pass one QueueConfig per logical domain (e.g. one for
// "payments", one for "notifications").
type QueueConfig struct {
	// ── Exchange ─────────────────────────────────────────────────────────────

	// Exchange is the AMQP exchange to bind the queue to.
	// Empty string falls back to BrokerConfig.DefaultExchange.
	Exchange string

	// ExchangeType is the AMQP exchange type. Default: "topic".
	// Valid values: "direct", "fanout", "topic", "headers".
	ExchangeType string

	// ── Queue ────────────────────────────────────────────────────────────────

	// Queue is the AMQP queue name. Required.
	Queue string

	// RoutingKeys are the binding keys from Exchange to Queue.
	// At least one is required. Supports AMQP wildcards: * (one word), # (many).
	//
	// Examples:
	//   []string{"payment.created", "payment.updated"}   exact keys
	//   []string{"payment.*"}                            all payment events
	//   []string{"#"}                                    catch-all (avoid in prod)
	RoutingKeys []string

	// ── Dead-Letter ───────────────────────────────────────────────────────────

	// DLX is the dead-letter exchange name.
	// Empty string → auto-derived as "<Exchange>.dlx".
	DLX string

	// DLQ is the dead-letter queue name.
	// Empty string → auto-derived as "<Queue>.dlq".
	DLQ string

	// ── Message Policy ────────────────────────────────────────────────────────

	// MessageTTL is the per-message TTL in the queue.
	// 0 = no TTL (messages live until consumed or dead-lettered).
	// Uses int64 milliseconds internally — no int32 overflow at ~24.8 days.
	MessageTTL time.Duration

	// MaxPriority enables priority queuing (0–255). 0 = disabled.
	// Enabling priority queuing on an existing queue requires queue redeclaration.
	MaxPriority uint8

	// Durable controls queue durability. Default: true.
	// Non-durable queues are lost on broker restart — almost always wrong in prod.
	Durable *bool // pointer so the zero value (false) is distinguishable from unset
}

// dlxName returns the effective dead-letter exchange name.
func (q *QueueConfig) dlxName(defaultExchange string) string {
	if q.DLX != "" {
		return q.DLX
	}
	ex := q.Exchange
	if ex == "" {
		ex = defaultExchange
	}
	return ex + ".dlx"
}

// dlqName returns the effective dead-letter queue name.
func (q *QueueConfig) dlqName() string {
	if q.DLQ != "" {
		return q.DLQ
	}
	return q.Queue + ".dlq"
}

// exchangeType returns the effective AMQP exchange type.
func (q *QueueConfig) exchangeType() string {
	if q.ExchangeType != "" {
		return q.ExchangeType
	}
	return amqp.ExchangeTopic
}

// durable returns whether the queue should be durable (default: true).
func (q *QueueConfig) durable() bool {
	if q.Durable == nil {
		return true
	}
	return *q.Durable
}

// Validate returns an error if the config is unusable.
func (q *QueueConfig) Validate() error {
	if q.Queue == "" {
		return fmt.Errorf("%w: Queue is required", ErrInvalidTopology)
	}
	if len(q.RoutingKeys) == 0 {
		return fmt.Errorf("%w: at least one RoutingKey is required for queue %q",
			ErrInvalidTopology, q.Queue)
	}
	for i, rk := range q.RoutingKeys {
		if rk == "" {
			return fmt.Errorf("%w: RoutingKeys[%d] must not be empty for queue %q",
				ErrInvalidTopology, i, q.Queue)
		}
	}
	return nil
}

// DeclareTopology idempotently creates the exchange, queue, DLX/DLQ, and all
// bindings described by cfg on a temporary channel. Safe to call concurrently
// from multiple modules at startup, and on every reconnect.
//
// Each call allocates and closes its own AMQP channel in isolation —
// topology declaration is infrequent so there is no need to pool these.
func (r *RabbitMQ) DeclareTopology(ctx context.Context, cfg QueueConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	ch, err := r.rawChannel()
	if err != nil {
		return fmt.Errorf("rabbitmq: DeclareTopology: %w", err)
	}
	defer ch.Close()

	exchange := cfg.Exchange
	if exchange == "" {
		exchange = r.cfg.DefaultExchange
	}
	dlxName := cfg.dlxName(r.cfg.DefaultExchange)
	dlqName := cfg.dlqName()
	durable := cfg.durable()

	// 1. Main exchange
	if err := ch.ExchangeDeclare(
		exchange, cfg.exchangeType(),
		durable, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("rabbitmq: declare exchange %q: %w", exchange, err)
	}

	// 2. Dead-letter exchange (always topic so the DLQ wildcard binding works)
	if err := ch.ExchangeDeclare(
		dlxName, amqp.ExchangeTopic,
		durable, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("rabbitmq: declare dlx %q: %w", dlxName, err)
	}

	// 3. Dead-letter queue
	if _, err := ch.QueueDeclare(dlqName, durable, false, false, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: declare dlq %q: %w", dlqName, err)
	}

	// 4. DLQ wildcard binding — catches all keys regardless of original routing key
	if err := ch.QueueBind(dlqName, "#", dlxName, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: bind dlq %q to dlx %q: %w", dlqName, dlxName, err)
	}

	// 5. Main queue
	mainArgs := amqp.Table{
		"x-dead-letter-exchange": dlxName,
	}
	if cfg.MessageTTL > 0 {
		// int64 milliseconds — int32 overflows at ~24.8 days.
		mainArgs["x-message-ttl"] = cfg.MessageTTL.Milliseconds()
	}
	if cfg.MaxPriority > 0 {
		mainArgs["x-max-priority"] = int32(cfg.MaxPriority)
	}

	if _, err := ch.QueueDeclare(
		cfg.Queue, durable, false, false, false, mainArgs,
	); err != nil {
		return fmt.Errorf("rabbitmq: declare queue %q: %w", cfg.Queue, err)
	}

	// 6. Bindings — one per routing key, all pointing to the main exchange
	for _, rk := range cfg.RoutingKeys {
		if err := ch.QueueBind(cfg.Queue, rk, exchange, false, nil); err != nil {
			return fmt.Errorf("rabbitmq: bind queue %q routing_key=%q exchange=%q: %w",
				cfg.Queue, rk, exchange, err)
		}
	}

	r.metrics.topologyDeclaredTotal.Inc()
	r.log.Info("rabbitmq: topology declared",
		"exchange", exchange,
		"exchange_type", cfg.exchangeType(),
		"queue", cfg.Queue,
		"routing_keys", cfg.RoutingKeys,
		"dlx", dlxName,
		"dlq", dlqName,
	)
	return nil
}
