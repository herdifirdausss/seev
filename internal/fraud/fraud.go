// Package fraud owns synchronous fraud screening and its audit events.
package fraud

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc"

	fraudv1 "github.com/herdifirdausss/seev/gen/fraud/v1"
	"github.com/herdifirdausss/seev/internal/fraud/grpcserver"
	"github.com/herdifirdausss/seev/internal/fraud/model"
	"github.com/herdifirdausss/seev/internal/fraud/repository"
	"github.com/herdifirdausss/seev/internal/fraud/rules"
	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/messaging"
)

type ScreenInput = model.ScreenInput
type Verdict = model.Verdict
type ScreeningEvent = model.ScreeningEvent

type Config struct {
	Mode               string
	AmountThreshold    decimal.Decimal
	VelocityMaxPerHour int64
}

type Module struct {
	repo   repository.ScreeningRepository
	rules  []rules.Rule
	store  VelocityStore
	broker Broker
	logger *slog.Logger
	cancel context.CancelFunc
}

func (m *Module) RegisterGRPC(server *grpc.Server) {
	fraudv1.RegisterFraudServiceServer(server, grpcserver.New(m))
}

type Broker interface {
	messaging.Consumer
	messaging.TopologyManager
}

const (
	velocityQueue       = "ledger.events.fraud"
	velocityConsumerTag = "fraud-velocity-consumer"
	velocityTTL         = 2 * time.Hour
)

func NewModule(db database.DatabaseSQL, store VelocityStore, broker Broker, cfg Config, logger *slog.Logger) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	repo := repository.NewScreeningRepository(db)
	mode := rules.ParseMode(cfg.Mode)
	module := &Module{repo: repo, store: store, broker: broker, logger: logger}
	if mode == rules.ModeOff {
		return module
	}
	if cfg.AmountThreshold.IsPositive() {
		module.rules = append(module.rules, rules.NewAmountThresholdRule(cfg.AmountThreshold, mode, repo, logger))
	}
	if cfg.VelocityMaxPerHour > 0 && store != nil {
		module.rules = append(module.rules, rules.NewVelocityAnomalyRule(cfg.VelocityMaxPerHour, mode, store, repo, logger))
	}
	return module
}

func (m *Module) Start(ctx context.Context) error {
	if m.broker == nil || m.store == nil {
		return nil
	}
	if err := m.broker.DeclareTopology(ctx, messaging.QueueConfig{
		Queue: velocityQueue, RoutingKeys: []string{events.TypeTransactionPosted},
	}); err != nil {
		return fmt.Errorf("fraud: declare topology: %w", err)
	}
	consumeCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	go func() {
		if err := m.broker.Consume(consumeCtx, messaging.ConsumeOptions{
			Queue: velocityQueue, ConsumerTag: velocityConsumerTag,
			PrefetchCount: 10, MaxDeliveryAttempts: 5,
		}, m.handleDelivery); err != nil {
			m.logger.Error("fraud: velocity consumer stopped", "error", err)
		}
	}()
	return nil
}

func (m *Module) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *Module) handleDelivery(ctx context.Context, delivery amqp.Delivery) error {
	var event events.TransactionPosted
	if err := json.Unmarshal(delivery.Body, &event); err != nil {
		return fmt.Errorf("fraud: decode TransactionPosted: %w", err)
	}
	if event.UserID == nil {
		return nil
	}
	if delivery.MessageId == "" {
		return fmt.Errorf("fraud: message id is required")
	}
	at := event.OccurredAt
	if at.IsZero() {
		at = time.Now()
	}
	key := rules.VelocityKey(event.UserID.String(), at)
	if err := m.store.Record(ctx, delivery.MessageId, key, velocityTTL); err != nil {
		return fmt.Errorf("fraud: increment velocity: %w", err)
	}
	return nil
}

func (m *Module) Screen(ctx context.Context, input ScreenInput) (Verdict, error) {
	var finding Verdict
	for _, rule := range m.rules {
		verdict, err := rule.Screen(ctx, input)
		if err != nil || verdict.Block {
			return verdict, err
		}
		if finding.Reason == "" && verdict.Reason != "" {
			finding = verdict
		}
	}
	return finding, nil
}

func (m *Module) ListEvents(ctx context.Context, userID, verdict string, limit, offset int) ([]ScreeningEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	return m.repo.ListEvents(ctx, userID, verdict, limit, offset)
}
