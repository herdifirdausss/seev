package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/herdifirdausss/seev/internal/platform/scheduling"
)

type disputeExpirer interface {
	ExpireDueDisputes(context.Context, time.Time, string, string) (int64, error)
}

// DisputeExpiryJob closes overdue evidence windows through the same audited
// service path as a human resolution. It is deliberately separate from the
// outbox/schedule workers so a slow card-network queue cannot starve deadlines.
type DisputeExpiryJob struct {
	expirer disputeExpirer
	logger  *slog.Logger
	sched   *scheduler.Scheduler
}

func NewDisputeExpiryJob(expirer disputeExpirer, lock scheduler.LockProvider, logger *slog.Logger, loc *time.Location) *DisputeExpiryJob {
	if logger == nil {
		logger = slog.Default()
	}
	if loc == nil {
		loc = time.UTC
	}
	return &DisputeExpiryJob{expirer: expirer, logger: logger, sched: scheduler.NewScheduler(lock, scheduler.NewPrometheusMetrics(), scheduler.WithLocation(loc))}
}

func (j *DisputeExpiryJob) Start(ctx context.Context) error {
	return j.sched.Cron("chargeback-dispute-expiry", "* * * * *", func(ctx context.Context) error {
		if j.expirer == nil {
			return nil
		}
		n, err := j.expirer.ExpireDueDisputes(ctx, time.Now().UTC(), "ledger-dispute-expiry-worker", "evidence deadline expired")
		if err != nil {
			j.logger.Error("dispute expiry failed", slog.Any("error", err))
			return err
		}
		if n > 0 {
			j.logger.Info("disputes expired", slog.Int64("count", n))
		}
		return nil
	})
}

func (j *DisputeExpiryJob) Stop() { j.sched.Stop() }
