package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/herdifirdausss/seev/internal/platform/resilience/alerting"
	"github.com/herdifirdausss/seev/internal/platform/scheduling"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

var stuckStateGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "ledger", Name: "stuck_state_count", Help: "Current count of due or stuck money-flow records by state.",
}, []string{"state"})

type stuckStateReader interface {
	ReadStuckState(context.Context, time.Time) (model.StuckStateSnapshot, error)
}

type StuckStateScanner struct {
	reader  stuckStateReader
	logger  *slog.Logger
	alertFn alerting.AlertFunc
	sched   *scheduler.Scheduler
}

func NewStuckStateScanner(reader stuckStateReader, lock scheduler.LockProvider, logger *slog.Logger, loc *time.Location, alertFn alerting.AlertFunc) *StuckStateScanner {
	if logger == nil {
		logger = slog.Default()
	}
	if loc == nil {
		loc = time.UTC
	}
	return &StuckStateScanner{reader: reader, logger: logger, alertFn: alertFn, sched: scheduler.NewScheduler(lock, scheduler.NewPrometheusMetrics(), scheduler.WithLocation(loc))}
}

func (s *StuckStateScanner) Start(ctx context.Context) error {
	return s.sched.Cron("ledger-stuck-state-scanner", "* * * * *", s.run)
}

func (s *StuckStateScanner) Stop() { s.sched.Stop() }

func (s *StuckStateScanner) run(ctx context.Context) error {
	if s.reader == nil {
		return nil
	}
	snapshot, err := s.reader.ReadStuckState(ctx, time.Now().UTC())
	if err != nil {
		s.logger.Error("stuck-state scan failed", slog.Any("error", err))
		return err
	}
	stuckStateGauge.WithLabelValues("outbox").Set(float64(snapshot.OutboxPendingCount))
	stuckStateGauge.WithLabelValues("scheduled_due").Set(float64(snapshot.ScheduleDueCount))
	stuckStateGauge.WithLabelValues("scheduled_processing_expired").Set(float64(snapshot.ScheduleProcessingCount))
	stuckStateGauge.WithLabelValues("dispute_deadline").Set(float64(snapshot.DisputeDueCount))
	if snapshot.ScheduleProcessingCount > 0 || snapshot.DisputeDueCount > 0 {
		message := fmt.Sprintf("ledger stuck states: scheduled_processing_expired=%d dispute_deadline=%d", snapshot.ScheduleProcessingCount, snapshot.DisputeDueCount)
		s.logger.Warn(message)
		if s.alertFn != nil {
			if alertErr := s.alertFn(ctx, "warning", message); alertErr != nil {
				s.logger.Error("stuck-state alert failed", slog.Any("error", alertErr))
			}
		}
	}
	return nil
}
