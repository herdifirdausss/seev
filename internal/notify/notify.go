// Package notify is the public facade for the in-app notification inbox
// (docs/roadmap/archive/25 Task T4) — the first RabbitMQ CONSUMER in this codebase
// (every other module only publishes to the outbox). External code may
// only import this package; internal/notify/repository and
// internal/notify/model are private to the module (docs/roadmap/archive/01 Module
// Boundaries, enforced by boundary_test.go). The ONLY internal/ledger
// subpackage this module imports is internal/ledger/events — the
// versioned outbox payload contract any consumer may decode.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/internal/notify/model"
	"github.com/herdifirdausss/seev/internal/notify/repository"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/messaging"
)

// Notification is re-exported so callers never need to import
// internal/notify/model.
type Notification = model.Notification

const queueName = "ledger.events.notifications"
const consumerTag = "notify-consumer"

// notifiableTypes are the ledger transaction_type values that produce a
// user-facing notification (docs/roadmap/archive/25 Task T4 step 3). Every other
// TransactionPosted event is filtered out silently — acked, no row
// written.
var notifiableTypes = map[string]bool{
	"money_in":        true,
	"transfer_p2p":    true,
	"withdraw_settle": true,
	"withdraw_cancel": true,
}

// Broker is the subset of messaging.Broker the notify module depends on —
// a local structural interface (mirrors internal/payin's Poster pattern)
// so unit tests can inject a mock without a real AMQP connection.
type Broker interface {
	messaging.Consumer
	messaging.TopologyManager
}

// Module is the notify module's public facade.
type Module struct {
	repo   repository.Repository
	broker Broker
	logger *slog.Logger
	cancel context.CancelFunc
}

func NewModule(db database.DatabaseSQL, broker Broker, logger *slog.Logger) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	return &Module{
		repo:   repository.NewRepository(db),
		broker: broker,
		logger: logger,
	}
}

// Start declares the queue topology, then launches the consumer in its own
// goroutine (docs/roadmap/archive/25 Task T4 step 3). Returns an error only if
// topology declaration itself fails; the consumer goroutine's own errors
// are logged, not returned (it self-heals via messaging.RabbitMQ.Consume's
// built-in reconnect/backoff loop).
func (m *Module) Start(ctx context.Context) error {
	if err := m.broker.DeclareTopology(ctx, messaging.QueueConfig{
		Queue:       queueName,
		RoutingKeys: []string{events.TypeTransactionPosted},
	}); err != nil {
		return fmt.Errorf("notify: declare topology: %w", err)
	}

	consumeCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	go func() {
		if err := m.broker.Consume(consumeCtx, messaging.ConsumeOptions{
			Queue:               queueName,
			ConsumerTag:         consumerTag,
			PrefetchCount:       10,
			MaxDeliveryAttempts: 5,
		}, m.handleDelivery); err != nil {
			m.logger.Error("notify: consumer stopped", "error", err)
		}
	}()
	return nil
}

// Stop cancels the consumer goroutine. Safe to call even if Start was
// never called or failed — cancel is nil-checked.
func (m *Module) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

// handleDelivery is the messaging.HandlerFunc bound to queueName: decode →
// filter → fan out to recipient(s) → dedup-insert. Every returned error is
// a plain (non-Retriable) error — a malformed payload or a permanent
// decode failure will never succeed differently on redelivery, so it
// routes straight to the DLQ rather than being requeued forever. A
// transient DB error also returns plain (not Retriable): RabbitMQ's own
// redelivery-on-first-nack behavior (pkg/messaging's shouldRequeue: retry
// once, then DLQ on redelivery) already gives one free retry without this
// handler needing to distinguish transient from permanent DB failures
// itself.
func (m *Module) handleDelivery(ctx context.Context, d amqp.Delivery) error {
	var ev events.TransactionPosted
	if err := json.Unmarshal(d.Body, &ev); err != nil {
		return fmt.Errorf("notify: decode TransactionPosted: %w", err)
	}

	if !notifiableTypes[ev.TransactionType] {
		return nil
	}

	eventID, err := uuid.Parse(d.MessageId)
	if err != nil {
		return fmt.Errorf("notify: invalid message id %q: %w", d.MessageId, err)
	}

	for _, rcpt := range recipientsFor(ev) {
		n := model.Notification{
			ID:      generalutil.NewV7(),
			UserID:  rcpt.userID,
			EventID: eventID,
			Type:    ev.TransactionType,
			Title:   rcpt.title,
			Body:    rcpt.body,
			Payload: d.Body,
		}
		if _, err := m.repo.Insert(ctx, n); err != nil {
			return fmt.Errorf("notify: insert notification: %w", err)
		}
	}
	return nil
}

type recipient struct {
	userID uuid.UUID
	title  string
	body   string
}

// recipientsFor maps one TransactionPosted event to its notification
// recipient(s) (docs/roadmap/archive/25 Task T4 step 3): money_in/withdraw_settle/
// withdraw_cancel notify the single UserID; transfer_p2p notifies BOTH
// parties with distinct sender/receiver copies ("sent"/"received"). An
// event with the relevant *UserID field unset produces zero recipients for
// that side — nothing to notify, not an error (defensive: every current
// processor for these four types always sets UserID, docs/roadmap/archive/25 T4's own
// enrichment step, but this must not panic if a future processor forgets).
func recipientsFor(ev events.TransactionPosted) []recipient {
	var out []recipient
	switch ev.TransactionType {
	case "transfer_p2p":
		if ev.UserID != nil {
			out = append(out, recipient{
				userID: *ev.UserID,
				title:  "Transfer sent",
				body:   fmt.Sprintf("Your %s %s transfer was sent successfully.", ev.Currency, ev.Amount),
			})
		}
		if ev.TargetUserID != nil {
			out = append(out, recipient{
				userID: *ev.TargetUserID,
				title:  "Transfer received",
				body:   fmt.Sprintf("You received a %s %s transfer.", ev.Currency, ev.Amount),
			})
		}
	case "money_in":
		if ev.UserID != nil {
			out = append(out, recipient{
				userID: *ev.UserID,
				title:  "Funds received",
				body:   fmt.Sprintf("Your %s %s top-up was successful and your balance increased.", ev.Currency, ev.Amount),
			})
		}
	case "withdraw_settle":
		if ev.UserID != nil {
			out = append(out, recipient{
				userID: *ev.UserID,
				title:  "Withdrawal successful",
				body:   fmt.Sprintf("Your %s %s withdrawal was processed successfully.", ev.Currency, ev.Amount),
			})
		}
	case "withdraw_cancel":
		if ev.UserID != nil {
			out = append(out, recipient{
				userID: *ev.UserID,
				title:  "Withdrawal canceled",
				body:   fmt.Sprintf("Your %s %s withdrawal was canceled and the funds were returned.", ev.Currency, ev.Amount),
			})
		}
	}
	return out
}

// ListNotifications returns userID's own notifications, newest first,
// keyset-paginated on created_at. before.IsZero() starts from the most
// recent. limit<=0 defaults to 50, capped at 200.
func (m *Module) ListNotifications(ctx context.Context, userID uuid.UUID, limit int, before time.Time) ([]Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	return m.repo.List(ctx, userID, limit, before)
}

// MarkRead marks id as read for userID (ownership enforced at the SQL
// layer). Returns ErrNotificationNotFound if no such row exists for that
// (id, userID) pair.
func (m *Module) MarkRead(ctx context.Context, id, userID uuid.UUID) error {
	matched, err := m.repo.MarkRead(ctx, id, userID)
	if err != nil {
		return err
	}
	if !matched {
		return ErrNotificationNotFound
	}
	return nil
}
