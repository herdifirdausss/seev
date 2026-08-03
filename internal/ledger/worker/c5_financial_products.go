package worker

import (
	"context"
	"log/slog"
	"time"

	interestservice "github.com/herdifirdausss/seev/internal/ledger/service/interest"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

type c5FinancialProductRunner interface {
	RunInterestDaily(context.Context, time.Time) interestservice.DailyRunSummary
	RunInterestPeriodCloseDue(context.Context, time.Time, string) (int, int, error)
}

// C5FinancialProductJob is an opt-in calendar worker for the C5 daily
// accrual/period-close lifecycle. It is separate from the legacy accrual job
// so deployments can roll out the schema and maker/checker controls first.
type C5FinancialProductJob struct {
	runner c5FinancialProductRunner
	logger *slog.Logger
	sched  *scheduler.Scheduler
	loc    *time.Location
}

func NewC5FinancialProductJob(runner c5FinancialProductRunner, lock scheduler.LockProvider, logger *slog.Logger, loc *time.Location) *C5FinancialProductJob {
	if logger == nil {
		logger = slog.Default()
	}
	if loc == nil {
		loc = time.UTC
	}
	return &C5FinancialProductJob{
		runner: runner, logger: logger, loc: loc,
		sched: scheduler.NewScheduler(lock, scheduler.NewPrometheusMetrics(), scheduler.WithLocation(loc)),
	}
}

func (j *C5FinancialProductJob) Start(ctx context.Context) error {
	// SnapshotJob materializes the previous local calendar day at 00:15.  Run
	// after that dependency and after the configured 01:15 close-not-before
	// boundary so the same durable job can recover both daily accruals and the
	// prior month's close.
	return j.sched.Cron("c5-financial-products", "20 1 * * *", j.runDaily)
}

func (j *C5FinancialProductJob) Stop() { j.sched.Stop() }

func (j *C5FinancialProductJob) runDaily(ctx context.Context) error {
	now := time.Now().In(j.loc)
	accrualDate := now.AddDate(0, 0, -1)
	// The interface intentionally returns an opaque summary so the worker
	// package does not depend on Ledger's model types; durable rows remain the
	// operational evidence.
	j.runner.RunInterestDaily(ctx, accrualDate)
	closed, failed, err := j.runner.RunInterestPeriodCloseDue(ctx, now.UTC(), "c5-period-close-worker")
	if err != nil {
		j.logger.Error("c5 financial products: close sweep failed", slog.Any("error", err))
		return err
	}
	j.logger.Info("c5 financial products: daily run complete", slog.String("date", accrualDate.Format("2006-01-02")), slog.Int("periods_closed", closed), slog.Int("periods_failed", failed))
	return nil
}
