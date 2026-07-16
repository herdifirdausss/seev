package model

import (
	"time"

	"github.com/google/uuid"
)

// Notification is one row of notif_notifications (docs/plan/25 Task T4) —
// one user's copy of a ledger TransactionPosted event. A two-party
// transaction (transfer_p2p) produces two independent rows, one per
// (EventID, UserID) — UNIQUE(event_id, user_id) is the at-least-once
// dedup guard against RabbitMQ redelivery of the same outbox event.
type Notification struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	EventID   uuid.UUID
	Type      string // ledger transaction_type: money_in, transfer_p2p, withdraw_settle, withdraw_cancel
	Title     string
	Body      string
	Payload   []byte // raw JSON — the decoded events.TransactionPosted, forensic/debug only
	ReadAt    *time.Time
	CreatedAt time.Time
}
