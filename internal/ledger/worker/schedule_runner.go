package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// scheduleRunner is the minimal surface ScheduleRunnerJob needs from
// service/schedule.Service — kept narrow (rather than importing the whole
// package) so this file's dependency footprint stays obvious.
type scheduleRunner interface {
	RunDue(ctx context.Context, asOf time.Time) (executed, failed int, err error)
}

// ScheduleRunnerJob runs docs/roadmap/archive/19 Task T1's daily job: post every
// scheduled_transactions row due today. Unlike SnapshotJob, there is no
// day-by-day catch-up loop on Start — a missed calendar day for a
// recurring (daily/monthly) schedule is not individually backfilled (the
// next due-check naturally still finds the row due, since
// last_run_date < asOf holds for any asOf after the last real run — see
// ScheduledTransactionRepository.ListDue), it just posts once for TODAY
// when the job finally runs again, not once per missed day. This is a
// deliberate MVP simplification: schedules represent recurring FUTURE
// action, not a historical record that must be reconstructed exactly
// (unlike balance snapshots).
type ScheduleRunnerJob struct {
	runner scheduleRunner
	logger *slog.Logger
	sched  *scheduler.Scheduler
	loc    *time.Location
}

// NewScheduleRunnerJob constructs a ScheduleRunnerJob. lock should be
// scheduler.NewRedisLock in production (one replica runs the job) or
// scheduler.NewMemoryLock for a single-instance deployment.
func NewScheduleRunnerJob(runner scheduleRunner, lock scheduler.LockProvider, logger *slog.Logger, loc *time.Location) *ScheduleRunnerJob {
	if logger == nil {
		logger = slog.Default()
	}
	if loc == nil {
		loc = time.UTC
	}
	return &ScheduleRunnerJob{
		runner: runner, logger: logger, loc: loc,
		sched: scheduler.NewScheduler(lock, scheduler.NewPrometheusMetrics(), scheduler.WithLocation(loc)),
	}
}

// Start registers the daily cron — 00:30 Asia/Jakarta, after the balance
// snapshot job (00:15) so schedules that read balance state are never
// racing an incomplete snapshot for the day that just closed (docs/roadmap/archive/19
// Task T1 step 4). Call Stop to shut down.
func (j *ScheduleRunnerJob) Start(ctx context.Context) error {
	return j.sched.Cron("schedule-runner", "30 0 * * *", j.runDaily)
}

// Stop stops the underlying scheduler, waiting for any in-flight run.
func (j *ScheduleRunnerJob) Stop() {
	j.sched.Stop()
}

// RunNow executes RunDue for the given date immediately, outside the cron
// schedule — backs the admin ops/testing endpoint
// (POST /admin/schedules/run?date=, docs/roadmap/archive/19 Task T1 step 5). NOT
// guarded by the distributed lock the cron path uses: an operator
// triggering this explicitly is trusted to not fire it concurrently from
// multiple replicas, and Post()'s own idempotency key makes even that safe
// (just redundant work), never a double-post.
func (j *ScheduleRunnerJob) RunNow(ctx context.Context, asOf time.Time) (executed, failed int, err error) {
	return j.runner.RunDue(ctx, asOf)
}

func (j *ScheduleRunnerJob) runDaily(ctx context.Context) error {
	today := time.Now().In(j.loc)
	executed, failed, err := j.runner.RunDue(ctx, today)
	if err != nil {
		j.logger.Error("schedule-runner: RunDue failed", slog.Any("error", err))
		return err
	}
	j.logger.Info("schedule-runner: run complete", slog.String("date", today.Format("2006-01-02")),
		slog.Int("executed", executed), slog.Int("failed", failed))
	return nil
}
