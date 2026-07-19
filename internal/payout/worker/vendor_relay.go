package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultDispatchPollInterval   = 1 * time.Second
	defaultDispatchRetryInterval  = 30 * time.Second
	defaultDispatchReaperInterval = 5 * time.Minute
	defaultDispatchStuckAfter     = 10 * time.Minute
	defaultDispatchBatchSize      = 20

	// dispatchCallTimeout bounds every claim/reap/gauge round trip made
	// from the relay's own background goroutines — mirrors
	// internal/ledger/worker.OutboxRelay's dbCallTimeout. It does NOT bound
	// the dispatch call itself (that's the vendor round trip inside
	// dispatchOne, which owns its own timeout via ctx passed through from
	// here — see internal/payout/relay.go).
	dispatchCallTimeout = 5 * time.Second
)

// VendorRelayConfig tunes the relay's timing. Zero values fall back to the
// defaults above.
type VendorRelayConfig struct {
	PollInterval   time.Duration
	RetryInterval  time.Duration
	ReaperInterval time.Duration
	StuckAfter     time.Duration
	BatchSize      int
}

func (c *VendorRelayConfig) applyDefaults() {
	if c.PollInterval <= 0 {
		c.PollInterval = defaultDispatchPollInterval
	}
	if c.RetryInterval <= 0 {
		c.RetryInterval = defaultDispatchRetryInterval
	}
	if c.ReaperInterval <= 0 {
		c.ReaperInterval = defaultDispatchReaperInterval
	}
	if c.StuckAfter <= 0 {
		c.StuckAfter = defaultDispatchStuckAfter
	}
	if c.BatchSize <= 0 {
		c.BatchSize = defaultDispatchBatchSize
	}
}

// dispatcher is the minimal surface VendorRelay needs from payout.Module —
// kept narrow rather than importing the whole package, same convention as
// resumer above and internal/ledger/worker.OutboxRelay's own repo
// interface. Each claim-shaped method claims a batch AND dispatches every
// claimed command before returning — the relay package itself has no
// access to the domain logic (settle/cancel/recordVendorCall) that
// dispatch requires, so unlike the ledger outbox relay it cannot claim and
// dispatch as two separate steps; see internal/payout/relay.go.
type dispatcher interface {
	// DispatchPendingCommands claims and dispatches up to limit 'pending'
	// commands, returning how many were claimed.
	DispatchPendingCommands(ctx context.Context, limit int) (int, error)
	// DispatchFailedCommandsForRetry is DispatchPendingCommands' sibling
	// for 'failed' commands whose backoff has elapsed.
	DispatchFailedCommandsForRetry(ctx context.Context, limit int) (int, error)
	// ReapStuckCommands returns lease-expired 'processing' commands to
	// 'failed' for an immediate retry.
	ReapStuckCommands(ctx context.Context, olderThan time.Duration) (int, error)
	// CountCommandsByStatuses feeds the payout_vendor_commands gauge
	// (docs/plan/45 K6).
	CountCommandsByStatuses(ctx context.Context, statuses []string) (map[string]int, error)
}

// VendorRelay runs docs/plan/45 Task T1's durable vendor-dispatch outbox
// relay: it polls payout_vendor_commands for 'pending' rows and 'failed'
// rows whose backoff has elapsed, dispatches each to its vendor via
// payout.Module (the only place provider.Submit is ever called from), and
// reaps commands whose claim lease expired without a result. Same
// four-loop shape as internal/ledger/worker.OutboxRelay (poll/retry/reap/
// gauge) — safe to run from multiple payout-service replicas concurrently,
// since claiming uses FOR UPDATE SKIP LOCKED at the repository layer.
type VendorRelay struct {
	dispatcher dispatcher
	logger     *slog.Logger
	cfg        VendorRelayConfig

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewVendorRelay constructs a relay. Call Start to begin polling and Stop
// to shut down gracefully (waits for the in-flight batch to finish).
func NewVendorRelay(d dispatcher, logger *slog.Logger, cfg VendorRelayConfig) *VendorRelay {
	if logger == nil {
		logger = slog.Default()
	}
	cfg.applyDefaults()
	return &VendorRelay{dispatcher: d, logger: logger, cfg: cfg}
}

// Start launches the relay's background loops. Safe to call once; call
// Stop before calling Start again.
func (r *VendorRelay) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	r.wg.Add(3)
	go r.loop(ctx, r.cfg.PollInterval, r.dispatcher.DispatchPendingCommands)
	go r.loop(ctx, r.cfg.RetryInterval, r.dispatcher.DispatchFailedCommandsForRetry)
	go r.reapLoop(ctx)
	r.wg.Add(1)
	go r.gaugeLoop(ctx)
}

// Stop cancels the background loops and waits for the current batch (if
// any) to finish processing before returning.
func (r *VendorRelay) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
}

type dispatchFunc func(ctx context.Context, limit int) (int, error)

func (r *VendorRelay) loop(ctx context.Context, interval time.Duration, dispatch dispatchFunc) {
	defer r.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Unlike the ledger outbox relay's claim step, dispatch itself
			// makes real vendor network calls per command — no fixed
			// per-call timeout is applied here (that would abort a
			// legitimately slow-but-successful vendor round trip); the
			// vendor provider is expected to enforce its own timeout.
			// Per-outcome attempt counting (docs/plan/45 K6's
			// payout_vendor_command_attempts_total{outcome}) happens inside
			// payout.Module.dispatchOne, which is the only place that
			// actually knows the outcome — this loop only knows how many
			// commands were claimed.
			if _, err := dispatch(ctx, r.cfg.BatchSize); err != nil {
				r.logger.Error("payout-relay: dispatch batch failed", slog.Any("error", err))
			}
		}
	}
}

func (r *VendorRelay) reapLoop(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.cfg.ReaperInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reapCtx, cancel := context.WithTimeout(ctx, dispatchCallTimeout)
			n, err := r.dispatcher.ReapStuckCommands(reapCtx, r.cfg.StuckAfter)
			cancel()
			if err != nil {
				r.logger.Error("payout-relay: reap stuck commands failed", slog.Any("error", err))
				continue
			}
			if n > 0 {
				vendorCommandsReapedTotal.Add(float64(n))
				r.logger.Warn("payout-relay: reaped stuck commands", slog.Int("count", n))
			}
		}
	}
}

const dispatchGaugeRefreshInterval = 15 * time.Second

// commandStatuses is every payout_vendor_commands.status value the gauge
// reports on (docs/plan/45 K6) — a status with zero rows is still reported
// as 0, never silently absent.
var commandStatuses = []string{"pending", "processing", "failed", "completed", "dead"}

func (r *VendorRelay) gaugeLoop(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(dispatchGaugeRefreshInterval)
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

func (r *VendorRelay) refreshGauges(ctx context.Context) {
	countCtx, cancel := context.WithTimeout(ctx, dispatchCallTimeout)
	defer cancel()
	counts, err := r.dispatcher.CountCommandsByStatuses(countCtx, commandStatuses)
	if err != nil {
		r.logger.Error("payout-relay: gauge refresh failed", slog.Any("error", err))
		return
	}
	for _, status := range commandStatuses {
		vendorCommandsGauge.WithLabelValues(status).Set(float64(counts[status]))
	}
}
