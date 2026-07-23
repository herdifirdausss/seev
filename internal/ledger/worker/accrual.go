package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// accrualRunner is the minimal surface AccrualJob needs from
// service/accrual.Service.
type accrualRunner interface {
	RunDue(ctx context.Context, asOf time.Time) (accrued, skipped int)
}

// AccrualJob runs docs/roadmap/archive/19 Task T3's daily job: compute and post
// interest for every enabled savings account, using yesterday's closing
// balance SNAPSHOT as the basis (never a live balance).
type AccrualJob struct {
	runner accrualRunner
	logger *slog.Logger
	sched  *scheduler.Scheduler
	loc    *time.Location
}

// NewAccrualJob constructs an AccrualJob. lock should be
// scheduler.NewRedisLock in production (one replica runs the job) or
// scheduler.NewMemoryLock for a single-instance deployment.
func NewAccrualJob(runner accrualRunner, lock scheduler.LockProvider, logger *slog.Logger, loc *time.Location) *AccrualJob {
	if logger == nil {
		logger = slog.Default()
	}
	if loc == nil {
		loc = time.UTC
	}
	return &AccrualJob{
		runner: runner, logger: logger, loc: loc,
		sched: scheduler.NewScheduler(lock, scheduler.NewPrometheusMetrics(), scheduler.WithLocation(loc)),
	}
}

// Start registers the daily cron — 00:45 Asia/Jakarta, after the balance
// snapshot job (00:15, the data dependency) and the schedule runner (00:30,
// docs/roadmap/archive/19 Task T3 step 4). Call Stop to shut down.
func (j *AccrualJob) Start(ctx context.Context) error {
	return j.sched.Cron("interest-accrual", "45 0 * * *", j.runDaily)
}

// Stop stops the underlying scheduler, waiting for any in-flight run.
func (j *AccrualJob) Stop() {
	j.sched.Stop()
}

// runDaily accrues interest for yesterday (Asia/Jakarta) — the cron fires
// at 00:45, so "yesterday" relative to that fire time is the calendar day
// that just fully closed, matching the snapshot job's own "yesterday"
// convention.
func (j *AccrualJob) runDaily(ctx context.Context) error {
	yesterday := time.Now().In(j.loc).AddDate(0, 0, -1)
	accrued, skipped := j.runner.RunDue(ctx, yesterday)
	j.logger.Info("interest-accrual: run complete", slog.String("date", yesterday.Format("2006-01-02")),
		slog.Int("accrued", accrued), slog.Int("skipped", skipped))
	return nil
}
