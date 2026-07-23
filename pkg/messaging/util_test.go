package messaging

import (
	"context"
	"testing"

	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/stretchr/testify/assert"
)

// ─── correlationIDFromContext ─────────────────────────────────────────────────
// docs/roadmap/archive/36 Task T4: publish must set AMQP CorrelationId from whichever
// mechanism the caller used — an explicit WithCorrelationID always wins (a
// caller correlating on something other than the HTTP/gRPC request_id), else
// it falls back to middleware.RequestIDFromCtx so a caller that never called
// WithCorrelationID still gets automatic tracing.

func TestCorrelationIDFromContext_PrefersExplicitCorrelationID(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.RequestIDKey, "http-request-id")
	ctx = WithCorrelationID(ctx, "explicit-business-id")

	assert.Equal(t, "explicit-business-id", correlationIDFromContext(ctx))
}

func TestCorrelationIDFromContext_FallsBackToRequestID(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.RequestIDKey, "http-request-id")

	assert.Equal(t, "http-request-id", correlationIDFromContext(ctx))
}

func TestCorrelationIDFromContext_EmptyWhenNeitherSet(t *testing.T) {
	assert.Empty(t, correlationIDFromContext(context.Background()))
}
