package messaging

// publisher.go — Publish, PublishTo implementation.
//
// All publish operations are exchange-agnostic: the exchange is resolved from
// PublishOptions.Exchange → BrokerConfig.DefaultExchange in that priority order.
// This means a single broker instance can publish to multiple exchanges without
// coupling any exchange name to the BrokerConfig.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// Publish marshals payload as JSON and publishes to BrokerConfig.DefaultExchange
// using routingKey. An idempotency MessageID is auto-generated (UUID v4).
func (r *RabbitMQ) Publish(ctx context.Context, routingKey string, payload any) error {
	return r.PublishTo(ctx, PublishOptions{RoutingKey: routingKey}, payload)
}

// PublishTo is the primary publish method. It accepts full PublishOptions to
// override exchange, routing key, and idempotency key on a per-message basis.
//
// Steps:
//  1. Acquire publish semaphore (backpressure against slow broker)
//  2. Resolve exchange and message-ID defaults
//  3. Marshal payload to JSON
//  4. Acquire a confirm-mode channel from the pool
//  5. Publish and wait for broker confirm within PublishTimeout
//  6. Return channel to pool (or discard on error)
//
// # mandatory=false — by design
//
// We intentionally do NOT use mandatory=true. With mandatory=true, a
// Basic.Return frame races with Basic.Ack in the amqp091-go library across
// separate goroutines, with no guaranteed ordering (amqp091-go/discussions/52).
// Without wiring NotifyReturn with DeliveryTag correlation, a confirmed-but-
// returned message would return nil — silent data loss.
//
// Instead: topology is always pre-declared by DeclareTopology(). If exchange +
// queue + binding exist, mandatory is unnecessary. Publisher confirms (Nack) and
// DLX/DLQ provide the safety net at the queue boundary.
func (r *RabbitMQ) PublishTo(ctx context.Context, opts PublishOptions, payload any) error {
	// Resolve defaults before span creation so they appear in span attributes.
	if opts.Exchange == "" {
		opts.Exchange = r.cfg.DefaultExchange
	}
	if opts.MessageID == "" {
		opts.MessageID = uuid.New().String()
	}

	if err := opts.validate(); err != nil {
		return &PublishError{
			Exchange: opts.Exchange, RoutingKey: opts.RoutingKey,
			MessageID: opts.MessageID, Cause: err,
		}
	}

	ctx, span := r.tracer.Start(ctx, "rabbitmq.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			semconv.MessagingSystemRabbitmq,
			semconv.MessagingDestinationName(opts.RoutingKey),
			attribute.String("messaging.message_id", opts.MessageID),
			attribute.String("messaging.rabbitmq.exchange", opts.Exchange),
		),
	)
	defer span.End()

	// ── 1. Acquire publish semaphore ─────────────────────────────────────────
	// Limits the number of goroutines concurrently blocked on confirm-wait,
	// preventing goroutine exhaustion when the broker is slow or overloaded.
	select {
	case r.publishSem <- struct{}{}:
		defer func() { <-r.publishSem }()
	case <-ctx.Done():
		return &PublishError{
			Exchange: opts.Exchange, RoutingKey: opts.RoutingKey,
			MessageID: opts.MessageID,
			Cause:     fmt.Errorf("acquire publish semaphore: %w", ctx.Err()),
		}
	}

	start := time.Now()

	// ── 2. Marshal payload ───────────────────────────────────────────────────
	body, err := json.Marshal(payload)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "marshal failed")
		return &PublishError{
			Exchange: opts.Exchange, RoutingKey: opts.RoutingKey,
			MessageID: opts.MessageID, Cause: fmt.Errorf("marshal: %w", err),
		}
	}

	// ── 3. Acquire confirm channel ───────────────────────────────────────────
	cc, err := r.chanPool.acquire()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "channel unavailable")
		r.metrics.publishTotal.WithLabelValues(opts.RoutingKey, "error").Inc()
		r.metrics.channelErrorTotal.Inc()
		return &PublishError{
			Exchange: opts.Exchange, RoutingKey: opts.RoutingKey,
			MessageID: opts.MessageID, Cause: err,
		}
	}
	hadError := false
	defer func() { r.chanPool.release(cc, hadError) }()

	// ── 4. Inject OTel trace context into AMQP headers ───────────────────────
	headers := amqp.Table{}
	otel.GetTextMapPropagator().Inject(ctx, amqpHeaderCarrier(headers))

	pub := amqp.Publishing{
		MessageId:     opts.MessageID,
		CorrelationId: correlationIDFromContext(ctx),
		AppId:         r.cfg.AppID,
		ContentType:   "application/json",
		DeliveryMode:  amqp.Persistent,
		Timestamp:     time.Now().UTC(),
		Headers:       headers,
		Body:          body,
	}

	// NOTE: PublishWithContext does NOT honour ctx for the network write itself
	// (amqp091-go docs: equivalent to Publish). ctx governs the confirm-wait
	// select below — where actual latency accumulates.
	publishCtx, cancel := context.WithTimeout(ctx, r.cfg.PublishTimeout)
	defer cancel()

	// ── 5. Publish ────────────────────────────────────────────────────────────
	if err := cc.ch.PublishWithContext(
		publishCtx,
		opts.Exchange, opts.RoutingKey,
		false, // mandatory: intentionally false — see package doc
		false, // immediate: deprecated in AMQP 0-9-1, always false
		pub,
	); err != nil {
		hadError = true
		span.RecordError(err)
		span.SetStatus(codes.Error, "publish failed")
		r.metrics.publishTotal.WithLabelValues(opts.RoutingKey, "error").Inc()
		r.metrics.channelErrorTotal.Inc()
		return &PublishError{
			Exchange: opts.Exchange, RoutingKey: opts.RoutingKey,
			MessageID: opts.MessageID, Cause: fmt.Errorf("channel publish: %w", err),
		}
	}

	// ── 6. Wait for publisher confirm ─────────────────────────────────────────
	select {
	case confirm, ok := <-cc.confirms:
		if !ok {
			hadError = true
			span.SetStatus(codes.Error, "confirms channel closed")
			r.metrics.publishTotal.WithLabelValues(opts.RoutingKey, "error").Inc()
			r.metrics.channelErrorTotal.Inc()
			return &PublishError{
				Exchange: opts.Exchange, RoutingKey: opts.RoutingKey,
				MessageID: opts.MessageID, Cause: ErrConfirmChannelClosed,
			}
		}
		if !confirm.Ack {
			// Nack: discard channel conservatively to avoid delivery-tag drift.
			hadError = true
			span.SetStatus(codes.Error, "broker nacked")
			r.metrics.publishTotal.WithLabelValues(opts.RoutingKey, "nack").Inc()
			return &PublishError{
				Exchange: opts.Exchange, RoutingKey: opts.RoutingKey,
				MessageID: opts.MessageID, Cause: ErrBrokerNacked,
			}
		}

	case <-publishCtx.Done():
		hadError = true
		span.SetStatus(codes.Error, "timeout waiting for confirm")
		r.metrics.publishTotal.WithLabelValues(opts.RoutingKey, "error").Inc()
		return &PublishError{
			Exchange: opts.Exchange, RoutingKey: opts.RoutingKey,
			MessageID: opts.MessageID,
			Cause:     fmt.Errorf("%w: %v", ErrPublishTimeout, publishCtx.Err()),
		}
	}

	elapsed := time.Since(start).Seconds()
	r.metrics.publishDuration.WithLabelValues(opts.Exchange, opts.RoutingKey).Observe(elapsed)
	r.metrics.publishTotal.WithLabelValues(opts.RoutingKey, "ok").Inc()
	span.SetStatus(codes.Ok, "published")
	return nil
}
