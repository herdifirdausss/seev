package model

import (
	"time"

	"github.com/google/uuid"
)

type OutboxEvent struct {
	AggregateType string
	AggregateID   uuid.UUID
	EventType     string
	Payload       map[string]any
}

// OutboxEventRecord is a claimed outbox_events row, read by the relay worker
// (internal/ledger/worker) for publishing to the message broker.
type OutboxEventRecord struct {
	ID            uuid.UUID
	AggregateType string
	AggregateID   uuid.UUID
	EventType     string
	Payload       map[string]any
	RetryCount    int
}

// DeadOutboxEvent is one 'dead' outbox_events row, listing-only (docs/roadmap/archive/25
// Task T5) — deliberately excludes Payload: an operator deciding whether to
// replay needs id/type/retry_count/last_error/created_at, not the full
// event body, and a "dead" list is exactly the rows most likely to be large
// batch/reversal payloads worth NOT shipping over the wire by default.
type DeadOutboxEvent struct {
	ID         uuid.UUID
	EventType  string
	RetryCount int
	LastError  string
	CreatedAt  time.Time
}
