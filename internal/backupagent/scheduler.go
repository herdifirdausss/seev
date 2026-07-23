package backupagent

import (
	"context"
	"time"

	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// StartScheduler registers the K4 baseline policy — one full backup
// Sunday at 02:10 Asia/Jakarta, one differential Monday through Saturday
// at 02:10 — and returns the running *scheduler.Scheduler so the caller
// can Stop() it during graceful shutdown. Overlap rejection and bounded
// execution time come from pkg/scheduler itself (distributed lock +
// per-job timeout), not from anything backup-agent adds on top.
//
// A single in-memory lock is enough here: backup-agent is deliberately
// never run with more than one replica (K13 — "a minimal internal
// process", not a horizontally-scaled domain service), so the
// multi-instance Redis-backed LockProvider other schedulers in this repo
// use is unnecessary complexity for this one.
func (a *Agent) StartScheduler(loc *time.Location) (*scheduler.Scheduler, error) {
	lock := scheduler.NewMemoryLock(time.Minute)
	sched := scheduler.NewScheduler(lock, scheduler.NewPrometheusMetrics(), scheduler.WithLocation(loc))

	if err := sched.Cron("backup-full", a.cfg.FullCronSpec, func(ctx context.Context) error {
		return a.RunBackup(ctx, "full")
	}, scheduler.WithJobTimeout(a.cfg.FullBackupTimeout)); err != nil {
		return nil, err
	}
	if err := sched.Cron("backup-diff", a.cfg.DiffCronSpec, func(ctx context.Context) error {
		return a.RunBackup(ctx, "diff")
	}, scheduler.WithJobTimeout(a.cfg.DiffBackupTimeout)); err != nil {
		return nil, err
	}
	return sched, nil
}
