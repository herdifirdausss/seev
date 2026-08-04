package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/herdifirdausss/seev/internal/platform/scheduling"
	interestservice "github.com/herdifirdausss/seev/services/ledger/internal/ledger/interest"
)

type financialProductRunner interface {
	RunInterestDaily(context.Context, time.Time) interestservice.DailyRunSummary
	RunInterestPeriodCloseDue(context.Context, time.Time, string) (int, int, error)
}

// FinancialProductJob is an opt-in calendar worker for the C5 daily
// accrual/period-close lifecycle. It is separate from the legacy accrual job
// so deployments can roll out the schema and maker/checker controls first.
type FinancialProductJob struct {
	runner financialProductRunner
	logger *slog.Logger
	sched  *scheduler.Scheduler
	loc    *time.Location
}

func NewFinancialProductJob(runner financialProductRunner, lock scheduler.LockProvider, logger *slog.Logger, loc *time.Location) *FinancialProductJob {
	if logger == nil {
		logger = slog.Default()
	}
	if loc == nil {
		loc = time.UTC
	}
	return &FinancialProductJob{
		runner: runner, logger: logger, loc: loc,
		sched: scheduler.NewScheduler(lock, scheduler.NewPrometheusMetrics(), scheduler.WithLocation(loc)),
	}
}

func (j *FinancialProductJob) Start(ctx context.Context) error {
	// SnapshotJob materializes the previous local calendar day at 00:15.  Run
	// after that dependency and after the configured 01:15 close-not-before
	// boundary so the same durable job can recover both daily accruals and the
	// prior month's close.
	return j.sched.Cron("financial-products", "20 1 * * *", j.runDaily)
}

func (j *FinancialProductJob) Stop() { j.sched.Stop() }

func (j *FinancialProductJob) runDaily(ctx context.Context) error {
	now := time.Now().In(j.loc)
	accrualDate := now.AddDate(0, 0, -1)
	// The interface intentionally returns an opaque summary so the worker
	// package does not depend on Ledger's model types; durable rows remain the
	// operational evidence.
	j.runner.RunInterestDaily(ctx, accrualDate)
	closed, failed, err := j.runner.RunInterestPeriodCloseDue(ctx, now.UTC(), "period-close-worker")
	if err != nil {
		j.logger.Error("c5 financial products: close sweep failed", slog.Any("error", err))
		return err
	}
	j.logger.Info("c5 financial products: daily run complete", slog.String("date", accrualDate.Format("2006-01-02")), slog.Int("periods_closed", closed), slog.Int("periods_failed", failed))
	return nil
}
