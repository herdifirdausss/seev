package messaging

// consumer.go — queue-agnostic consumer.
//
// Design goals:
//
//	1. Queue is a runtime parameter (ConsumeOptions.Queue), not wired into
//	   broker config — multiple modules can consume different queues from
//	   the same broker instance.
//
//	2. PrefetchCount and MaxDeliveryAttempts are per-consumer — a low-
//	   throughput notification queue can set prefetch=5 while a high-
//	   throughput event queue sets prefetch=200, without touching broker config.
//
//	3. Consumer tag is collision-proof: a stable UUID segment is appended at
//	   the start of each Consume() call and never reset. This prevents
//	   RESOURCE_LOCKED when the broker hasn't yet deregistered the previous
//	   consumer tag (race on fast reconnect).
//
//	4. handleDelivery tracks every in-flight call in RabbitMQ.inFlight so
//	   Close() can drain them before tearing down the connection.

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/pkg/middleware"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// Consume starts a self-healing consumer for the queue specified in opts.
// It blocks until ctx is cancelled or the broker is closed.
//
// Recovery behaviour:
//
//	delivery channel closed   → restart consumeOnce with backoff
//	broker disconnected       → reconnectLoop re-establishes conn; consumer
//	                            session then fails and restarts independently
//	ctx cancelled / Close()   → clean shutdown, no restart
//
// Each call to Consume should run in its own goroutine. Multiple calls with
// different opts.Queue values consume from different queues concurrently,
// all over the same shared connection.
//
//	go broker.Consume(ctx, messaging.ConsumeOptions{
//	    Queue:               "payments.queue",
//	    ConsumerTag:         "payment-processor",
//	    PrefetchCount:       50,
//	    MaxDeliveryAttempts: 5,
//	}, paymentHandler)
//
//	go broker.Consume(ctx, messaging.ConsumeOptions{
//	    Queue:               "notifications.queue",
//	    ConsumerTag:         "notification-sender",
//	    PrefetchCount:       10,
//	    MaxDeliveryAttempts: 3,
//	}, notificationHandler)
func (r *RabbitMQ) Consume(
	ctx context.Context,
	opts ConsumeOptions,
	handler HandlerFunc,
) error {
	if err := opts.validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConsumeOptions, err)
	}
	opts.applyDefaults()

	// Stable UUID base — generated once per Consume() call, never reset.
	// Prevents RESOURCE_LOCKED if the broker hasn't deregistered the previous
	// tag before we reconnect and re-register.
	sessionBase := fmt.Sprintf("%s.%s", opts.ConsumerTag, uuid.New().String()[:8])

	attempt := 0
	for {
		attempt++
		tag := fmt.Sprintf("%s.%d", sessionBase, attempt)

		err := r.consumeOnce(ctx, tag, opts, handler)

		// Clean shutdown paths — no restart.
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-r.done:
			return nil
		default:
		}

		if err != nil {
			r.log.Error("rabbitmq: consumer session ended, restarting",
				"consumer", sessionBase,
				"queue", opts.Queue,
				"attempt", attempt,
				"error", err,
			)
			delay := backoffDelay(attempt, r.cfg.ReconnectBaseDelay)
			select {
			case <-ctx.Done():
				return nil
			case <-r.done:
				return nil
			case <-time.After(delay):
			}
			continue
		}
		return nil
	}
}

// consumeOnce runs one consumer session on a single AMQP channel.
// Returns when the delivery channel closes or ctx/done fires.
func (r *RabbitMQ) consumeOnce(
	ctx context.Context,
	consumerTag string,
	opts ConsumeOptions,
	handler HandlerFunc,
) error {
	ch, err := r.rawChannel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.Qos(opts.PrefetchCount, 0, false); err != nil {
		return fmt.Errorf("rabbitmq consume [queue=%s]: qos: %w", opts.Queue, err)
	}

	deliveries, err := ch.Consume(
		opts.Queue, consumerTag,
		false, // auto-ack: always false — we ack/nack explicitly in handleDelivery
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,
	)
	if err != nil {
		return fmt.Errorf("rabbitmq consume [queue=%s consumer=%s]: %w",
			opts.Queue, consumerTag, err)
	}

	r.log.Info("rabbitmq: consumer started",
		"queue", opts.Queue,
		"consumer", consumerTag,
		"prefetch", opts.PrefetchCount,
		"max_delivery_attempts", opts.MaxDeliveryAttempts,
	)
	r.metrics.activeConsumers.WithLabelValues(opts.Queue).Inc()
	defer r.metrics.activeConsumers.WithLabelValues(opts.Queue).Dec()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.done:
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return fmt.Errorf(
					"rabbitmq: delivery channel closed [queue=%s consumer=%s]",
					opts.Queue, consumerTag)
			}
			r.handleDelivery(ctx, d, opts, handler)
		}
	}
}

// handleDelivery processes one delivery, applies DLQ logic, and acks/nacks.
//
// Panic recovery: any handler panic is caught, logged, and routes the message
// to DLQ (nack without requeue). The consumer session continues uninterrupted.
func (r *RabbitMQ) handleDelivery(
	ctx context.Context,
	d amqp.Delivery,
	opts ConsumeOptions,
	handler HandlerFunc,
) {
	// Track in-flight count so Close() can drain before connection teardown.
	r.inFlight.Add(1)
	defer r.inFlight.Done()

	// Extract OTel trace context from AMQP headers so the consumer span is
	// a child of the publisher span — full end-to-end trace visibility.
	parentCtx := otel.GetTextMapPropagator().Extract(ctx, amqpHeaderCarrier(d.Headers))

	handlerCtx, span := r.tracer.Start(parentCtx, "rabbitmq.consume",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			semconv.MessagingSystemRabbitmq,
			semconv.MessagingDestinationName(opts.Queue),
			attribute.String("messaging.message_id", d.MessageId),
			attribute.String("messaging.rabbitmq.routing_key", d.RoutingKey),
			attribute.Bool("messaging.rabbitmq.redelivered", d.Redelivered),
			attribute.Int("messaging.rabbitmq.prefetch", opts.PrefetchCount),
		),
	)
	defer span.End()

	// Restore the request_id carried in CorrelationId (set by the publisher
	// via correlationIDFromContext) before invoking the handler, so a
	// consumer's own logs/outbound calls/persisted rows can trace back to
	// the HTTP/gRPC request that originally triggered the publish
	// (docs/plan/36 Task T4) — the publishing ctx is long gone by the time
	// this delivery is processed, so CorrelationId is the only carrier.
	if d.CorrelationId != "" {
		handlerCtx = context.WithValue(handlerCtx, middleware.RequestIDKey, d.CorrelationId)
	}

	start := time.Now()

	// ── DLQ guard: max delivery attempts ─────────────────────────────────────
	// totalDeathCount sums ALL x-death entries, not just [0], so multi-queue
	// death cycles are counted correctly.
	if count := totalDeathCount(d); count >= int64(opts.MaxDeliveryAttempts) {
		r.log.Error("rabbitmq: max delivery attempts exceeded, routing to DLQ",
			"message_id", d.MessageId,
			"routing_key", d.RoutingKey,
			"death_count", count,
			"max", opts.MaxDeliveryAttempts,
			"queue", opts.Queue,
		)
		span.SetStatus(codes.Error, "max delivery attempts exceeded")
		r.metrics.dlqTotal.WithLabelValues(opts.Queue, "max_attempts").Inc()
		r.metrics.consumeTotal.WithLabelValues(opts.Queue, "dlq").Inc()
		_ = d.Nack(false, false)
		return
	}

	// ── Panic recovery ────────────────────────────────────────────────────────
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("rabbitmq: handler panic",
				"panic", p,
				"message_id", d.MessageId,
				"queue", opts.Queue,
			)
			span.SetStatus(codes.Error, "handler panic")
			r.metrics.dlqTotal.WithLabelValues(opts.Queue, "panic").Inc()
			r.metrics.consumeTotal.WithLabelValues(opts.Queue, "panic").Inc()
			_ = d.Nack(false, false)
		}
	}()

	// ── Invoke handler ────────────────────────────────────────────────────────
	err := handler(handlerCtx, d)
	elapsed := time.Since(start).Seconds()
	r.metrics.consumeDuration.WithLabelValues(opts.Queue).Observe(elapsed)

	if err == nil {
		if ackErr := d.Ack(false); ackErr != nil {
			r.log.Error("rabbitmq: ack failed",
				"message_id", d.MessageId,
				"queue", opts.Queue,
				"error", ackErr,
			)
			span.RecordError(ackErr)
		}
		r.metrics.consumeTotal.WithLabelValues(opts.Queue, "ok").Inc()
		span.SetStatus(codes.Ok, "")
		return
	}

	// ── Error handling ────────────────────────────────────────────────────────
	span.RecordError(err)

	// Requeue only when: error is RetriableError AND first delivery (not redelivered).
	// On redelivery we assume the message is poisoned and route to DLQ.
	shouldRequeue := IsRetriable(err) && !d.Redelivered

	r.log.Error("rabbitmq: handler error",
		"message_id", d.MessageId,
		"routing_key", d.RoutingKey,
		"queue", opts.Queue,
		"redelivered", d.Redelivered,
		"requeue", shouldRequeue,
		"error", err,
	)

	if shouldRequeue {
		span.SetStatus(codes.Error, "handler error → requeue")
	} else {
		span.SetStatus(codes.Error, "handler error → DLQ")
		r.metrics.dlqTotal.WithLabelValues(opts.Queue, "handler_error").Inc()
	}
	r.metrics.consumeTotal.WithLabelValues(opts.Queue, "error").Inc()
	_ = d.Nack(false, shouldRequeue)
}

// totalDeathCount sums "count" across ALL x-death entries.
//
// RabbitMQ appends a new x-death entry each time a message is dead-lettered
// from a different queue. Reading only [0] (as v1 did) under-counts messages
// that cycle through multiple queues — a correctness bug at high retry depth.
func totalDeathCount(d amqp.Delivery) int64 {
	xDeath, ok := d.Headers["x-death"].([]interface{})
	if !ok {
		return 0
	}
	var total int64
	for _, entry := range xDeath {
		table, ok := entry.(amqp.Table)
		if !ok {
			continue
		}
		count, _ := table["count"].(int64)
		total += count
	}
	return total
}
