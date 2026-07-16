package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/alerting"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// maxCatchUpDays bounds how far back SnapshotJob.Start will walk to fill in
// missing snapshot dates on startup. A gap larger than this means something
// unusual happened (fresh restore from an old backup, the job disabled for
// weeks) — log a warning and stop, pointing at the runbook, rather than
// silently doing a month+ of backfill work inline with process startup
// (docs/plan/15 Task T1).
const maxCatchUpDays = 31

// SnapshotJob runs the daily balance snapshot (docs/plan/15 Task T1,
// decision K6): it writes account_balance_snapshots rows for the previous
// calendar day, then cross-checks them against account_balances.balance for
// accounts that haven't moved since — a mismatch there is a real projection
// bug. Unlike Verifier, this job WRITES data (INSERT ... ON CONFLICT DO
// NOTHING is idempotent, safe to re-run for the same date), so it is
// deliberately a separate type rather than folded into Verifier's
// detect-only responsibility.
type SnapshotJob struct {
	snapshotRepo repository.SnapshotRepository
	logger       *slog.Logger
	sched        *scheduler.Scheduler
	loc          *time.Location
	// alertFn mirrors Verifier's (docs/plan/12 Task T4) — may be nil.
	alertFn alerting.AlertFunc
}

// NewSnapshotJob constructs a SnapshotJob. lock should be
// scheduler.NewRedisLock in production (one replica runs the job) or
// scheduler.NewMemoryLock for a single-instance deployment.
func NewSnapshotJob(snapshotRepo repository.SnapshotRepository, lock scheduler.LockProvider, logger *slog.Logger, loc *time.Location, alertFn alerting.AlertFunc) *SnapshotJob {
	if logger == nil {
		logger = slog.Default()
	}
	if loc == nil {
		loc = time.UTC
	}
	return &SnapshotJob{
		snapshotRepo: snapshotRepo, logger: logger, loc: loc, alertFn: alertFn,
		sched: scheduler.NewScheduler(lock, nil, scheduler.WithLocation(loc)),
	}
}

func (j *SnapshotJob) alert(ctx context.Context, severity, message string) {
	if j.alertFn == nil {
		return
	}
	if err := j.alertFn(ctx, severity, message); err != nil {
		j.logger.Error("balance snapshot: alert delivery failed", slog.Any("error", err))
	}
}

// Start fills in any missing snapshot dates since the last run (bounded by
// maxCatchUpDays), then registers the daily cron (00:15 Asia/Jakarta —
// comfortably after midnight so the previous day's ledger activity has
// settled). Call Stop to shut down.
func (j *SnapshotJob) Start(ctx context.Context) error {
	if err := j.catchUp(ctx); err != nil {
		j.logger.Error("balance snapshot: catch-up failed", slog.Any("error", err))
	}
	return j.sched.Cron("balance-snapshot", "15 0 * * *", j.runDaily)
}

// Stop stops the underlying scheduler, waiting for any in-flight run.
func (j *SnapshotJob) Stop() {
	j.sched.Stop()
}

// runDaily snapshots yesterday (Asia/Jakarta) — the cron fires at 00:15, so
// "yesterday" relative to that fire time is the calendar day that just fully
// closed.
func (j *SnapshotJob) runDaily(ctx context.Context) error {
	yesterday := time.Now().In(j.loc).AddDate(0, 0, -1)
	return j.snapshotForDate(ctx, yesterday)
}

// catchUp fills gaps between the last snapshot date and yesterday — normal
// after a deploy that took the job offline for a few days, or the very first
// run ever (in which case LatestSnapshotDate returns not-found and catch-up
// no-ops, since there is no reference point; the first cron firing then
// starts the history from there).
func (j *SnapshotJob) catchUp(ctx context.Context) error {
	last, found, err := j.snapshotRepo.LatestSnapshotDate(ctx)
	if err != nil {
		return fmt.Errorf("catch-up: %w", err)
	}
	if !found {
		j.logger.Info("balance snapshot: no prior snapshots found, skipping catch-up (fresh start)")
		return nil
	}

	yesterday := time.Now().In(j.loc).AddDate(0, 0, -1)
	lastDate := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, j.loc)
	yesterdayDate := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, j.loc)

	gapDays := int(yesterdayDate.Sub(lastDate).Hours() / 24)
	if gapDays <= 0 {
		return nil
	}
	if gapDays > maxCatchUpDays {
		j.logger.Warn("balance snapshot: gap since last snapshot exceeds catch-up limit, not backfilling automatically",
			slog.Int("gap_days", gapDays), slog.Int("max_catch_up_days", maxCatchUpDays),
			slog.String("hint", "see docs/runbooks — likely a restored backup or the job was disabled for a long time; backfill manually"))
		return nil
	}

	for d := lastDate.AddDate(0, 0, 1); !d.After(yesterdayDate); d = d.AddDate(0, 0, 1) {
		if err := j.snapshotForDate(ctx, d); err != nil {
			return fmt.Errorf("catch-up %s: %w", d.Format("2006-01-02"), err)
		}
	}
	return nil
}

func (j *SnapshotJob) snapshotForDate(ctx context.Context, date time.Time) error {
	n, err := j.snapshotRepo.InsertForDate(ctx, date)
	if err != nil {
		return fmt.Errorf("insert for %s: %w", date.Format("2006-01-02"), err)
	}
	snapshotRowsTotal.Add(float64(n))
	j.logger.Info("balance snapshot: wrote rows", slog.String("date", date.Format("2006-01-02")), slog.Int("rows", n))

	mismatches, err := j.snapshotRepo.VerifyDate(ctx, date)
	if err != nil {
		return fmt.Errorf("verify %s: %w", date.Format("2006-01-02"), err)
	}
	if len(mismatches) == 0 {
		return nil
	}

	snapshotMismatchesTotal.Add(float64(len(mismatches)))
	for _, m := range mismatches {
		j.logger.Error("balance snapshot: mismatch vs current balance",
			slog.String("account_id", m.AccountID.String()),
			slog.String("as_of_date", m.AsOfDate.Format("2006-01-02")),
			slog.String("snapshot_balance", m.SnapshotBalance.String()),
			slog.String("current_balance", m.CurrentBalance.String()))
		j.alert(ctx, "critical", fmt.Sprintf(
			"balance snapshot mismatch: account_id=%s as_of=%s snapshot_balance=%s current_balance=%s",
			m.AccountID, m.AsOfDate.Format("2006-01-02"), m.SnapshotBalance, m.CurrentBalance))
	}
	// Deliberately does NOT overwrite the (possibly correct) snapshot with
	// the (possibly wrong) current balance, or vice versa — a mismatch means
	// a human needs to investigate which side is actually right
	// (docs/plan/15 Task T1). The job's job is to detect, not repair.
	return nil
}
