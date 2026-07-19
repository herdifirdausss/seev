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
	"github.com/herdifirdausss/seev/pkg/middleware"
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
	repo         repository.ScreeningRepository
	modeRepo     repository.RuleModeRepository
	modeResolver *ruleModeResolver
	rules        []rules.Rule
	store        VelocityStore
	broker       Broker
	logger       *slog.Logger
	cancel       context.CancelFunc
	spill        *eventSpill
	spillCancel  context.CancelFunc
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
	modeRepo := repository.NewRuleModeRepository(db)
	module := &Module{repo: repo, modeRepo: modeRepo, modeResolver: newRuleModeResolver(modeRepo, mode, logger), store: store, broker: broker, logger: logger, spill: newEventSpill()}
	if cfg.AmountThreshold.IsPositive() {
		module.rules = append(module.rules, rules.NewAmountThresholdRuleWithResolver(cfg.AmountThreshold, mode, module.modeResolver, repo, logger))
	}
	if cfg.VelocityMaxPerHour > 0 && store != nil {
		module.rules = append(module.rules, rules.NewVelocityAnomalyRuleWithResolver(cfg.VelocityMaxPerHour, mode, module.modeResolver, store, repo, logger))
	}
	return module
}

func (m *Module) Start(ctx context.Context) error {
	m.startSpillFlusher(ctx)
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
	if m.spillCancel != nil {
		m.spillCancel()
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
	// request_id here is the CorrelationId the publisher stamped on this
	// message (docs/plan/36 Task T4/T6) — logging it is what lets a trace
	// span the async hop from "HTTP/gRPC request that posted the
	// transaction" to "this velocity counter increment", the same way the
	// synchronous screening call already does via pkg/fraudcheck.
	m.logger.Info("fraud: velocity recorded", "request_id", middleware.RequestIDFromCtx(ctx), "user_id", event.UserID.String())
	return nil
}

func (m *Module) Screen(ctx context.Context, input ScreenInput) (Verdict, error) {
	var finding Verdict
	for _, rule := range m.rules {
		verdict, err := rule.Screen(ctx, input)
		if verdict.Event != nil {
			m.persistScreeningEvent(ctx, *verdict.Event)
		}
		if err != nil || verdict.Block {
			return verdict, err
		}
		if finding.Reason == "" && verdict.Reason != "" {
			finding = verdict
		}
	}
	return finding, nil
}

func (m *Module) persistScreeningEvent(ctx context.Context, event ScreeningEvent) {
	if m.repo == nil {
		m.enqueueSpill(event)
		return
	}
	if err := m.repo.InsertEvent(ctx, event); err != nil {
		logger := m.logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Error("fraud: persist screening event failed", slog.Any("error", err), slog.String("rule", event.Rule), slog.String("user_id", event.UserID.String()))
		m.enqueueSpill(event)
	}
}

func (m *Module) enqueueSpill(event ScreeningEvent) {
	if m.spill == nil {
		m.spill = newEventSpill()
	}
	beforeLost := m.spill.lostCount()
	m.spill.enqueue(event)
	fraudScreeningEventWriteFailures.Inc()
	fraudScreeningEventSpillDepth.Set(float64(m.spill.depth()))
	if m.spill.lostCount() > beforeLost {
		fraudScreeningEventsLost.Inc()
	}
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
