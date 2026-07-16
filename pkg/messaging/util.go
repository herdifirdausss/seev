package messaging

// backoff.go — exponential backoff with full jitter.
//
// Full jitter ("random(0, ceiling)") distributes reconnect storms uniformly.
// See: https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/
//
// Formula: jitter ∈ [0, min(base × 2^(attempt-1), base × 64))
//
// Examples (base = 1 s):
//
//	attempt 1 → ceiling 1 s  → jitter ∈ [0, 1 s)
//	attempt 2 → ceiling 2 s  → jitter ∈ [0, 2 s)
//	attempt 4 → ceiling 8 s  → jitter ∈ [0, 8 s)
//	attempt 8 → ceiling 64 s → jitter ∈ [0, 64 s)  (capped)

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/herdifirdausss/seev/pkg/middleware"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel/propagation"
)

// ─── Backoff ─────────────────────────────────────────────────────────────────

const (
	maxBackoffShift      = 6
	maxBackoffMultiplier = 1 << maxBackoffShift // 64
)

// jitterRand is a package-level RNG seeded at startup.
// Dedicated source avoids contention with other packages' global rand usage.
// A mutex makes concurrent calls safe without atomic overhead.
// Cryptographic quality is unnecessary for backoff jitter. #nosec G404
var (
	jitterMu   sync.Mutex
	jitterRand = rand.New(rand.NewSource(time.Now().UnixNano()))
)

func randInt63n(n int64) int64 {
	jitterMu.Lock()
	v := jitterRand.Int63n(n)
	jitterMu.Unlock()
	return v
}

func backoffDelay(attempt int, base time.Duration) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	shift := attempt - 1
	if shift > maxBackoffShift {
		shift = maxBackoffShift
	}
	ceiling := base * time.Duration(1<<shift)
	if ceiling <= 0 || ceiling > base*maxBackoffMultiplier {
		ceiling = base * maxBackoffMultiplier
	}
	return time.Duration(randInt63n(int64(ceiling)))
}

// ─── OTel Header Carrier ─────────────────────────────────────────────────────

// amqpHeaderCarrier adapts amqp.Table to propagation.TextMapCarrier.
//
// Inject is called on Publish (write trace context into AMQP headers).
// Extract is called on Consume (read trace context from AMQP headers).
//
// This makes the consumer span a child of the publisher span, giving
// end-to-end visibility across the queue boundary in tools like Jaeger.
type amqpHeaderCarrier amqp.Table

var _ propagation.TextMapCarrier = amqpHeaderCarrier{}

func (c amqpHeaderCarrier) Get(key string) string {
	if c == nil {
		return ""
	}
	v, ok := c[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func (c amqpHeaderCarrier) Set(key, value string) {
	if c != nil {
		c[key] = value
	}
}

func (c amqpHeaderCarrier) Keys() []string {
	if c == nil {
		return nil
	}
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// ─── Context Helpers ─────────────────────────────────────────────────────────

// correlationIDKey is an unexported type to prevent key collisions with other
// packages using context.WithValue.
type correlationIDKey struct{}

// WithCorrelationID attaches a correlation ID to ctx.
//
// The correlation ID flows through Publish → AMQP headers → Consume,
// enabling cross-service request tracing independent of OTel trace IDs.
// Use your transaction ID, order ID, or request ID here.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey{}, id)
}

// CorrelationIDFromContext extracts the correlation ID from ctx.
// Returns empty string if none was set.
func CorrelationIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(correlationIDKey{}).(string)
	return v
}

// correlationIDFromContext resolves the AMQP CorrelationId to publish: an
// explicit WithCorrelationID value takes priority (a caller correlating on
// something other than the HTTP/gRPC request_id, e.g. a business id), else
// it falls back to middleware.RequestIDFromCtx so a caller that never
// explicitly set a correlation id still gets one automatically — unifying
// the two mechanisms rather than requiring every publish site to plumb both
// (docs/plan/36 Task T4).
func correlationIDFromContext(ctx context.Context) string {
	if id := CorrelationIDFromContext(ctx); id != "" {
		return id
	}
	return middleware.RequestIDFromCtx(ctx)
}
