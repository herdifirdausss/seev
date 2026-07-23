// Package worker runs the ledger module's background jobs: the outbox relay
// (reliable event delivery to the broker) and the integrity verifier
// (docs/roadmap/archive/06-phase-1-workers.md). Both run as goroutines inside the
// ledger-service process for MVP (decision D9, docs/roadmap/archive/01) rather than as a
// separate binary.
package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/messaging"
)

const (
	defaultPollInterval   = 1 * time.Second
	defaultRetryInterval  = 30 * time.Second
	defaultReaperInterval = 5 * time.Minute
	defaultStuckAfter     = 10 * time.Minute
	defaultBatchSize      = 100

	// dbCallTimeout bounds every individual repository call made from the
	// relay's long-lived worker goroutines (docs/roadmap/archive/11 Task T5). Without
	// this, a single stuck query on one call would block that goroutine —
	// and therefore that entire loop (poll, retry, reap, or gauge) —
	// forever, since the parent ctx passed to Start has no deadline of its
	// own for the life of the process.
	dbCallTimeout = 5 * time.Second
)

// OutboxRelayConfig tunes the relay's timing. Zero values fall back to the
// defaults above.
type OutboxRelayConfig struct {
	PollInterval   time.Duration
	RetryInterval  time.Duration
	ReaperInterval time.Duration
	StuckAfter     time.Duration
	BatchSize      int
}

func (c *OutboxRelayConfig) applyDefaults() {
	if c.PollInterval <= 0 {
		c.PollInterval = defaultPollInterval
	}
	if c.RetryInterval <= 0 {
		c.RetryInterval = defaultRetryInterval
	}
	if c.ReaperInterval <= 0 {
		c.ReaperInterval = defaultReaperInterval
	}
	if c.StuckAfter <= 0 {
		c.StuckAfter = defaultStuckAfter
	}
	if c.BatchSize <= 0 {
		c.BatchSize = defaultBatchSize
	}
}

// OutboxRelay implements the transactional outbox relay pattern: it polls
// outbox_events for 'pending' rows, publishes each to the broker, and marks
// the result. Delivery is at-least-once — consumers must dedup on the AMQP
// MessageID (set to the outbox event's ID).
//
// Safe to run from multiple process replicas concurrently: claiming uses
// `FOR UPDATE SKIP LOCKED`, so two replicas never claim the same row.
type OutboxRelay struct {
	repo      repository.OutboxRepository
	publisher messaging.Publisher
	logger    *slog.Logger
	cfg       OutboxRelayConfig

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewOutboxRelay constructs a relay. Call Start to begin polling and Stop to
// shut down gracefully (waits for in-flight batches to finish).
func NewOutboxRelay(repo repository.OutboxRepository, publisher messaging.Publisher, logger *slog.Logger, cfg OutboxRelayConfig) *OutboxRelay {
	if logger == nil {
		logger = slog.Default()
	}
	cfg.applyDefaults()
	return &OutboxRelay{repo: repo, publisher: publisher, logger: logger, cfg: cfg}
}

// Start launches the relay's background loops. Safe to call once; call Stop
// before calling Start again.
func (r *OutboxRelay) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	r.wg.Add(4)
	go r.loop(ctx, r.cfg.PollInterval, r.repo.ClaimPending)
	go r.loop(ctx, r.cfg.RetryInterval, r.repo.ClaimFailedForRetry)
	go r.reapLoop(ctx)
	go r.gaugeLoop(ctx)
}

// Stop cancels the background loops and waits for the current batch (if
// any) to finish processing before returning.
func (r *OutboxRelay) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
}

type claimFunc func(ctx context.Context, limit int) ([]model.OutboxEventRecord, error)

func (r *OutboxRelay) loop(ctx context.Context, interval time.Duration, claim claimFunc) {
	defer r.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.processBatch(ctx, claim)
		}
	}
}

func (r *OutboxRelay) processBatch(ctx context.Context, claim claimFunc) {
	claimCtx, cancel := context.WithTimeout(ctx, dbCallTimeout)
	events, err := claim(claimCtx, r.cfg.BatchSize)
	cancel()
	if err != nil {
		r.logger.Error("outbox: claim batch failed", slog.Any("error", err))
		return
	}
	for _, e := range events {
		r.publishOne(ctx, e)
	}
}

func (r *OutboxRelay) publishOne(ctx context.Context, e model.OutboxEventRecord) {
	// The relay's ctx is the worker's own background loop context — it never
	// carries the originating HTTP/gRPC request_id (that ctx is long gone by
	// the time this event is claimed and published). Restore it from the
	// persisted payload instead, so the AMQP CorrelationId a consumer sees
	// still traces back to the request that caused the posting
	// (docs/roadmap/archive/36 Task T4).
	if requestID, ok := e.Payload["request_id"].(string); ok && requestID != "" {
		ctx = messaging.WithCorrelationID(ctx, requestID)
	}

	// PublishTo has its own internal timeout (RabbitMQConfig.PublishTimeout)
	// — not wrapped here, only the repository calls below are (T5 is about
	// bounding DB round trips specifically).
	err := r.publisher.PublishTo(ctx, messaging.PublishOptions{
		RoutingKey: e.EventType,
		MessageID:  e.ID.String(),
	}, e.Payload)

	if err != nil {
		outboxPublishFailuresTotal.Inc()
		markCtx, cancel := context.WithTimeout(ctx, dbCallTimeout)
		markErr := r.repo.MarkFailed(markCtx, e.ID, err.Error())
		cancel()
		if markErr != nil {
			r.logger.Error("outbox: mark failed error", slog.String("event_id", e.ID.String()), slog.Any("error", markErr))
			return
		}
		if e.RetryCount+1 >= 5 { // matches outbox_events.max_retries default; DB trigger is authoritative
			r.logger.Warn("outbox: event exhausted retries, likely dead",
				slog.String("event_id", e.ID.String()), slog.String("event_type", e.EventType))
		} else {
			r.logger.Warn("outbox: publish failed, will retry",
				slog.String("event_id", e.ID.String()), slog.String("event_type", e.EventType), slog.Any("error", err))
		}
		return
	}

	publishedCtx, cancel := context.WithTimeout(ctx, dbCallTimeout)
	err = r.repo.MarkPublished(publishedCtx, e.ID)
	cancel()
	if err != nil {
		r.logger.Error("outbox: mark published error", slog.String("event_id", e.ID.String()), slog.Any("error", err))
		return
	}
	outboxPublishedTotal.Inc()
}

func (r *OutboxRelay) reapLoop(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.cfg.ReaperInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reapCtx, cancel := context.WithTimeout(ctx, dbCallTimeout)
			n, err := r.repo.ReapStuck(reapCtx, r.cfg.StuckAfter)
			cancel()
			if err != nil {
				r.logger.Error("outbox: reap stuck failed", slog.Any("error", err))
				continue
			}
			if n > 0 {
				outboxReapedTotal.Add(float64(n))
				r.logger.Warn("outbox: reaped stuck events", slog.Int("count", n))
			}
		}
	}
}

const gaugeRefreshInterval = 15 * time.Second

func (r *OutboxRelay) gaugeLoop(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(gaugeRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.refreshGauges(ctx)
		}
	}
}

// refreshGauges updates outboxPendingGauge/outboxDeadGauge from a single
// query (docs/roadmap/archive/11 Task T6) instead of two sequential round trips.
// Split out from gaugeLoop so it's callable directly in tests without
// waiting on gaugeRefreshInterval's ticker.
func (r *OutboxRelay) refreshGauges(ctx context.Context) {
	countCtx, cancel := context.WithTimeout(ctx, dbCallTimeout)
	defer cancel()
	counts, err := r.repo.CountByStatuses(countCtx, []string{"pending", "dead"})
	if err != nil {
		r.logger.Error("outbox: gauge refresh failed", slog.Any("error", err))
		return
	}
	outboxPendingGauge.Set(float64(counts["pending"]))
	outboxDeadGauge.Set(float64(counts["dead"]))
}
