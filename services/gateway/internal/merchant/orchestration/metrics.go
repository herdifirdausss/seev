package merchant

// Plan 57 T9's own observability gauges — package-level so they register
// once regardless of how many Modules a process constructs, mirroring
// internal/platform/lifecycle/retention/worker/services/auth's own "refreshed once per worker
// tick" convention (services/auth/internal/auth/privacy_metrics.go). Labels are
// state/status only: NEVER a tenant id, delivery id, or idempotency key
// (T9 acceptance: "dashboards avoid tenant-level metric cardinality").
import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var idempotencyRecordsGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "seev",
	Subsystem: "merchant",
	Name:      "idempotency_records",
	Help:      "Current merchant_idempotency_records row count, by state (processing|completed|failed). Refreshed once per observability tick.",
}, []string{"state"})

var idempotencyStuckLeasesGauge = promauto.NewGauge(prometheus.GaugeOpts{
	Namespace: "seev",
	Subsystem: "merchant",
	Name:      "idempotency_stuck_leases",
	Help:      "Idempotency records still 'processing' whose lease has already expired — a crashed claimant nobody has taken over yet.",
})

var webhookDeliveriesGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "seev",
	Subsystem: "merchant",
	Name:      "webhook_deliveries",
	Help:      "Current merchant_webhook_deliveries row count, by status (pending|failed|delivered|dead). Refreshed once per observability tick.",
}, []string{"status"})

var webhookBacklogOldestAgeSeconds = promauto.NewGauge(prometheus.GaugeOpts{
	Namespace: "seev",
	Subsystem: "merchant",
	Name:      "webhook_backlog_oldest_age_seconds",
	Help:      "Age of the oldest pending-or-failed webhook delivery, in seconds — 0 when the backlog is empty.",
})

var b2bAPIEnabledGauge = promauto.NewGauge(prometheus.GaugeOpts{
	Namespace: "seev",
	Subsystem: "merchant",
	Name:      "b2b_api_enabled",
	Help:      "1 if the global merchant B2B API kill switch (services/gateway/internal/merchant/auth.GlobalFlag) is enabled, 0 if an operator has disabled it.",
})

// DefaultObservabilityRefreshInterval is how often StartObservabilityRefresher
// recomputes the gauges above (and reloads GlobalFlag — see
// refreshObservabilityGauges) when the caller doesn't need a different
// cadence.
const DefaultObservabilityRefreshInterval = 30 * time.Second

// StartObservabilityRefresher launches T9's periodic gauge-refresh loop —
// same Start/Stop lifecycle shape as StartWebhookRelay. Safe to call from
// multiple Gateway instances concurrently: every instance computes and
// republishes the same cluster-wide counts, which Prometheus's own
// last-scrape-wins semantics handle without any coordination.
func (m *Module) StartObservabilityRefresher(ctx context.Context, interval time.Duration, logger *slog.Logger) (stop func()) {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		m.refreshObservabilityGauges(ctx, logger)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.refreshObservabilityGauges(ctx, logger)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (m *Module) refreshObservabilityGauges(ctx context.Context, logger *slog.Logger) {
	if err := m.GlobalFlag.Refresh(ctx); err != nil {
		logger.Error("merchant: refresh global B2B-enabled flag failed", "error", err)
	}
	if m.GlobalFlag.Enabled() {
		b2bAPIEnabledGauge.Set(1)
	} else {
		b2bAPIEnabledGauge.Set(0)
	}

	if counts, err := m.Idempotency.StateCounts(ctx); err != nil {
		logger.Error("merchant: refresh idempotency state gauge failed", "error", err)
	} else {
		idempotencyRecordsGauge.Reset()
		for state, count := range counts {
			idempotencyRecordsGauge.WithLabelValues(state).Set(float64(count))
		}
	}

	if stuck, err := m.Idempotency.CountStuckLeases(ctx); err != nil {
		logger.Error("merchant: refresh idempotency stuck-lease gauge failed", "error", err)
	} else {
		idempotencyStuckLeasesGauge.Set(float64(stuck))
	}

	counts, oldestPendingAt, err := m.Webhooks.BacklogStats(ctx)
	if err != nil {
		logger.Error("merchant: refresh webhook backlog gauges failed", "error", err)
		return
	}
	webhookDeliveriesGauge.Reset()
	for status, count := range counts {
		webhookDeliveriesGauge.WithLabelValues(status).Set(float64(count))
	}
	if oldestPendingAt != nil {
		webhookBacklogOldestAgeSeconds.Set(time.Since(*oldestPendingAt).Seconds())
	} else {
		webhookBacklogOldestAgeSeconds.Set(0)
	}
}
