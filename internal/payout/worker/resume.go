// Package worker holds internal/payout's background job(s). Not part of the
// module's public facade — only internal/payout itself may import this
// package (docs/plan/01-target-architecture.md, enforced by
// boundary_test.go), mirroring internal/ledger/worker's boundary.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// resumer is the minimal surface ResumeJob needs from payout.Module — kept
// narrow rather than importing the whole package, same convention as
// internal/ledger/worker.scheduleRunner.
type resumer interface {
	ResumeStuck(ctx context.Context, olderThan time.Duration) (resumed, failed int, err error)
}

// ResumeJob runs docs/plan/23 Task T3 step 3's resume/polling job: unlike
// every other worker in this codebase (all daily cron jobs), a stuck payout
// request needs to be re-driven on the order of minutes, not once a day —
// money is parked in a hold account until the request reaches a terminal
// state, so this job is the crash-mid-flight recovery mechanism, not an
// optional housekeeping pass.
type ResumeJob struct {
	resumer   resumer
	logger    *slog.Logger
	sched     *scheduler.Scheduler
	olderThan time.Duration
}

// NewResumeJob constructs a ResumeJob. lock should be scheduler.NewRedisLock
// in production (one replica runs the job) or scheduler.NewMemoryLock for a
// single-instance deployment — same convention as every other job in this
// codebase. olderThan is how long a request must sit in 'submitted' or
// 'vendor_pending' before a resume pass re-drives it (avoids racing a
// submit/poll that is still genuinely in flight from the current attempt).
func NewResumeJob(r resumer, lock scheduler.LockProvider, logger *slog.Logger, olderThan time.Duration) *ResumeJob {
	if logger == nil {
		logger = slog.Default()
	}
	if olderThan <= 0 {
		olderThan = time.Minute
	}
	return &ResumeJob{
		resumer: r, logger: logger, olderThan: olderThan,
		sched: scheduler.NewScheduler(lock, nil),
	}
}

// Start registers the resume job on a one-minute cron cadence. Call Stop to
// shut down.
//
// WithJobTimeout(30s) overrides the scheduler's generic 5-minute default:
// that default sizes the distributed lock's TTL too (lockTTL = job timeout +
// buffer), so a resume pass that crashes mid-run (the exact scenario this
// job exists to recover from) would otherwise leave a stale Redis lock
// blocking every other replica's resume attempts for up to 5 minutes — five
// missed cron ticks on a job meant to re-drive stuck money within seconds.
// A resume pass is a handful of local DB queries plus a few vendor/ledger
// calls; 30s is generous headroom over that while keeping the self-heal
// window close to the job's own 1-minute cadence.
func (j *ResumeJob) Start(ctx context.Context) error {
	return j.sched.Cron("payout-resume", "* * * * *", j.runOnce, scheduler.WithJobTimeout(30*time.Second))
}

// Stop stops the underlying scheduler, waiting for any in-flight run.
func (j *ResumeJob) Stop() {
	j.sched.Stop()
}

// RunNow executes one resume pass immediately, outside the cron schedule —
// backs chaos tests (docs/plan/23 Task T6) and a future admin-triggered
// endpoint. Not guarded by the distributed lock the cron path uses, same
// rationale as ScheduleRunnerJob.RunNow: every downstream operation
// (submit/settle/cancel) is idempotent by request ID, so redundant
// concurrent runs are wasted work, never a double-post.
func (j *ResumeJob) RunNow(ctx context.Context) (resumed, failed int, err error) {
	return j.resumer.ResumeStuck(ctx, j.olderThan)
}

func (j *ResumeJob) runOnce(ctx context.Context) error {
	resumed, failed, err := j.resumer.ResumeStuck(ctx, j.olderThan)
	if err != nil {
		j.logger.Error("payout-resume: run failed", slog.Any("error", err))
		return err
	}
	if resumed > 0 || failed > 0 {
		j.logger.Info("payout-resume: run complete", slog.Int("resumed", resumed), slog.Int("failed", failed))
	}
	return nil
}
