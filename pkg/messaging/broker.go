package messaging

// broker.go — RabbitMQ struct, constructor, connection management, lifecycle.
//
// The broker manages exactly one *amqp.Connection shared across:
//   - Publisher (via confirm-channel pool)
//   - Consumer  (one channel per consumeOnce session)
//   - Topology  (one transient channel per DeclareTopology call)
//
// A reconnect loop watches for connection-close notifications and attempts
// to restore the connection with exponential backoff + full jitter.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// ─── Constants ────────────────────────────────────────────────────────────────

const (
	defaultDialTimeout          = 10 * time.Second
	defaultPublishTimeout       = 5 * time.Second
	defaultChannelPoolSize      = 16
	defaultMaxConcurrentPublish = 64
	defaultDrainTimeout         = 30 * time.Second
)

// ─── RabbitMQ ─────────────────────────────────────────────────────────────────

// RabbitMQ implements Broker. All exported methods are goroutine-safe.
//
// Obtain via New or NewWithRegistry — never construct directly.
type RabbitMQ struct {
	cfg     BrokerConfig
	dial    dialFn
	metrics *metrics
	tracer  trace.Tracer
	log     *slog.Logger

	// Connection state — guarded by mu.
	mu         sync.RWMutex
	conn       amqpConnection
	connClosed chan *amqp.Error

	// confirm-channel pool shared across all Publish callers.
	chanPool *confirmChannelPool

	// Publish semaphore: bounds concurrent goroutines waiting on confirms.
	publishSem chan struct{}

	// inFlight tracks active handleDelivery calls for graceful drain in Close().
	inFlight sync.WaitGroup

	done      chan struct{}
	closeOnce sync.Once
}

var _ Broker = (*RabbitMQ)(nil)

// ─── Constructors ─────────────────────────────────────────────────────────────

// New creates a RabbitMQ broker using the default Prometheus registry.
// Prefer NewWithRegistry in tests or when running multiple broker instances
// in the same process.
func New(ctx context.Context, cfg BrokerConfig) (*RabbitMQ, error) {
	return newWithDial(ctx, cfg, prometheus.DefaultRegisterer, defaultDial)
}

// NewWithRegistry creates a RabbitMQ broker with a caller-supplied Prometheus
// registerer. Use prometheus.NewRegistry() in tests to avoid metric conflicts.
func NewWithRegistry(
	ctx context.Context,
	cfg BrokerConfig,
	reg prometheus.Registerer,
) (*RabbitMQ, error) {
	return newWithDial(ctx, cfg, reg, defaultDial)
}

func newWithDial(
	ctx context.Context,
	cfg BrokerConfig,
	reg prometheus.Registerer,
	dial dialFn,
) (*RabbitMQ, error) {
	cfg = cfg.withDefaults()

	r := &RabbitMQ{
		cfg:        cfg,
		dial:       dial,
		metrics:    newMetrics(reg),
		tracer:     otel.Tracer("messaging/rabbitmq"),
		log:        slog.Default().With("component", "rabbitmq", "addr", cfg.safeAddr()),
		done:       make(chan struct{}),
		publishSem: make(chan struct{}, cfg.MaxConcurrentPublish),
	}

	// Pool factory closes over r — always allocates against the current r.conn.
	r.chanPool = newConfirmChannelPool(cfg.ChannelPoolSize, r.newConfirmedChannel)

	if err := r.connect(ctx); err != nil {
		return nil, err
	}

	go r.reconnectLoop(ctx)
	return r, nil
}

// ─── Connection Management ───────────────────────────────────────────────────

// connect dials the broker, registers a close-notification channel, and
// drains the confirm-channel pool (stale channels from the old connection).
func (r *RabbitMQ) connect(ctx context.Context) error {
	conn, err := r.dial(ctx, &r.cfg)
	if err != nil {
		r.metrics.connectionStatus.Set(0)
		return fmt.Errorf("rabbitmq: dial %s: %w", r.cfg.safeAddr(), err)
	}

	closedCh := make(chan *amqp.Error, 1)
	conn.NotifyClose(closedCh)

	r.mu.Lock()
	r.conn = conn
	r.connClosed = closedCh
	r.mu.Unlock()

	// Drain stale pool channels — they are bound to the old *amqp.Connection.
	if r.chanPool != nil {
		r.chanPool.drain()
	}

	r.metrics.connectionStatus.Set(1)
	r.log.Info("rabbitmq: connected")
	return nil
}

func (r *RabbitMQ) connClosedCh() chan *amqp.Error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.connClosed
}

func (r *RabbitMQ) reconnectLoop(ctx context.Context) {
	for {
		select {
		case <-r.done:
			return
		case <-ctx.Done():
			return
		case amqpErr, ok := <-r.connClosedCh():
			if !ok {
				return
			}
			r.metrics.connectionStatus.Set(0)
			r.log.Warn("rabbitmq: connection lost", "error", amqpErr)
			r.attemptReconnect(ctx)
		}
	}
}

func (r *RabbitMQ) attemptReconnect(ctx context.Context) {
	max := r.cfg.MaxReconnectAttempts
	for attempt := 1; attempt <= max || max == 0; attempt++ {
		r.metrics.reconnectTotal.Inc()
		delay := backoffDelay(attempt, r.cfg.ReconnectBaseDelay)

		r.log.Info("rabbitmq: reconnecting", "attempt", attempt, "delay", delay)

		select {
		case <-r.done:
			return
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		if err := r.connect(ctx); err != nil {
			r.log.Error("rabbitmq: reconnect failed", "attempt", attempt, "error", err)
			continue
		}

		r.log.Info("rabbitmq: reconnected", "attempt", attempt)
		return
	}

	r.log.Error("rabbitmq: exhausted reconnect attempts", "max", max)
}

// ─── Health & Lifecycle ───────────────────────────────────────────────────────

// HealthCheck returns nil if the AMQP connection is live and not closed.
// Suitable for Kubernetes readiness/liveness probes.
func (r *RabbitMQ) HealthCheck() error {
	r.mu.RLock()
	conn := r.conn
	r.mu.RUnlock()

	if conn == nil || conn.IsClosed() {
		return fmt.Errorf("rabbitmq: %w", ErrNotConnected)
	}
	return nil
}

// Close drains in-flight handlers, empties the channel pool, then closes
// the AMQP connection. Safe to call multiple times.
//
// Drain timeout is controlled by BrokerConfig.DrainTimeout (default: 30s).
// If handlers do not complete in time, the connection is force-closed.
// In-flight nacks caused by force-close are expected — the broker will
// re-deliver or dead-letter depending on queue policy.
func (r *RabbitMQ) Close() error {
	var closeErr error
	r.closeOnce.Do(func() {
		close(r.done)

		// ── Drain in-flight handlers ──────────────────────────────────────────
		drained := make(chan struct{})
		go func() {
			r.inFlight.Wait()
			close(drained)
		}()

		select {
		case <-drained:
			r.log.Info("rabbitmq: all in-flight handlers completed, closing")
		case <-time.After(r.cfg.DrainTimeout):
			r.log.Warn("rabbitmq: drain timeout, forcing close",
				"timeout", r.cfg.DrainTimeout)
		}

		// ── Close channel pool ────────────────────────────────────────────────
		r.chanPool.drain()

		// ── Close connection ──────────────────────────────────────────────────
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.conn != nil && !r.conn.IsClosed() {
			r.log.Info("rabbitmq: closing connection")
			r.metrics.connectionStatus.Set(0)
			closeErr = r.conn.Close()
		}
	})
	return closeErr
}

// ─── Internal Helpers ─────────────────────────────────────────────────────────

// rawChannel opens a plain (non-confirm-mode) AMQP channel.
//
// TOCTOU guard: if the connection closes between IsClosed() and Channel(),
// the resulting error is mapped to ErrNotConnected for a consistent sentinel.
func (r *RabbitMQ) rawChannel() (*amqp.Channel, error) {
	r.mu.RLock()
	conn := r.conn
	r.mu.RUnlock()

	if conn == nil || conn.IsClosed() {
		return nil, fmt.Errorf("rabbitmq: %w", ErrNotConnected)
	}
	ch, err := conn.Channel()
	if err != nil {
		if conn.IsClosed() {
			// TOCTOU: connection closed in the narrow window between check and call.
			return nil, fmt.Errorf("rabbitmq: %w", ErrNotConnected)
		}
		r.metrics.channelErrorTotal.Inc()
		return nil, fmt.Errorf("rabbitmq: open channel: %w", err)
	}
	r.metrics.channelOpenTotal.Inc()
	return ch, nil
}

// newConfirmedChannel allocates a fresh confirm-mode channel against the current
// connection. This is the factory wired into confirmChannelPool.
func (r *RabbitMQ) newConfirmedChannel() (*confirmedChannel, error) {
	r.mu.RLock()
	conn := r.conn
	r.mu.RUnlock()

	if conn == nil || conn.IsClosed() {
		return nil, fmt.Errorf("rabbitmq: %w", ErrNotConnected)
	}

	ch, err := conn.Channel()
	if err != nil {
		if conn.IsClosed() {
			return nil, fmt.Errorf("rabbitmq: %w", ErrNotConnected)
		}
		r.metrics.channelErrorTotal.Inc()
		return nil, fmt.Errorf("rabbitmq: open confirm channel: %w", err)
	}

	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		r.metrics.channelErrorTotal.Inc()
		return nil, fmt.Errorf("rabbitmq: confirm mode: %w", err)
	}

	r.metrics.channelOpenTotal.Inc()
	// Buffer of 1: one confirm delivered per publish, never blocking the broker.
	confirms := ch.NotifyPublish(make(chan amqp.Confirmation, 1))
	return &confirmedChannel{ch: ch, confirms: confirms}, nil
}

// ─── defaultDial ──────────────────────────────────────────────────────────────

// defaultDial establishes a real AMQP(S) connection.
//
// ctx is wired into net.Dialer so that a context cancellation or deadline
// interrupts a blocking TCP dial — not just the confirm-wait phase.
// This matters during shutdown or when the broker is unreachable for minutes.
func defaultDial(ctx context.Context, cfg *BrokerConfig) (amqpConnection, error) {
	amqpCfg := amqp.Config{
		Dial: func(network, addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: cfg.DialTimeout}
			return d.DialContext(ctx, network, addr)
		},
	}
	if cfg.TLS != nil {
		amqpCfg.TLSClientConfig = cfg.TLS
	}
	return amqp.DialConfig(cfg.url(), amqpCfg)
}

// ─── Testing pattern ─────────────────────────────────────────────────────────

// MockBroker implements Broker for unit tests.
// Generate with mockery or write by hand as shown.
type MockBroker struct {
	PublishFn         func(ctx context.Context, routingKey string, payload any) error
	PublishToFn       func(ctx context.Context, opts PublishOptions, payload any) error
	ConsumeFn         func(ctx context.Context, opts ConsumeOptions, h HandlerFunc) error
	DeclareTopologyFn func(ctx context.Context, cfg QueueConfig) error
	HealthCheckFn     func() error
}

func (m *MockBroker) Publish(ctx context.Context, rk string, p any) error {
	return m.PublishFn(ctx, rk, p)
}
func (m *MockBroker) PublishTo(ctx context.Context, opts PublishOptions, p any) error {
	return m.PublishToFn(ctx, opts, p)
}
func (m *MockBroker) Consume(ctx context.Context, opts ConsumeOptions, h HandlerFunc) error {
	return m.ConsumeFn(ctx, opts, h)
}
func (m *MockBroker) DeclareTopology(ctx context.Context, cfg QueueConfig) error {
	return m.DeclareTopologyFn(ctx, cfg)
}
func (m *MockBroker) HealthCheck() error { return m.HealthCheckFn() }
func (m *MockBroker) Close() error       { return nil }
