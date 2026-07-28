package retentionworker

import (
	"context"
	"fmt"
	"time"

	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// JakartaLocation is docs/roadmap/archive/50 and docs/roadmap/archive/51's shared reference
// timezone for every daily job's cron spec. Exported so each owner service
// can pass it to its own scheduler.WithLocation(...) when constructing the
// *scheduler.Scheduler it hands to Runner.Start — loaded once here rather
// than duplicated per service, matching internal/backupagent's own
// approach (falls back to a fixed UTC+7 offset if the platform's tzdata is
// unavailable, since Asia/Jakarta has no DST to get wrong).
var JakartaLocation = loadJakarta()

func loadJakarta() *time.Location {
	if loc, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		return loc
	}
	return time.FixedZone("WIB", 7*60*60)
}

// serviceJitterMinutes is docs/roadmap/archive/51 K6's "deterministic service
// jitter" — a fixed, distinct per-owner minute offset from 01:30 WIB so all
// eight services' daily retention runs don't all hit their own Postgres at
// the exact same instant. Deterministic (not random) so a run's exact
// scheduled time is reproducible from the owner name alone, matching this
// repository's existing fixed-offset convention for scheduled jobs (e.g.
// docs/roadmap/archive/50 K4's backup schedule).
var serviceJitterMinutes = map[string]int{
	"ledger":    0,
	"auth":      1,
	"payin":     2,
	"payout":    3,
	"fraud":     4,
	"gateway":   5,
	"adminbff":  6,
	"assurance": 7,
}

// JobName is the fixed name every owner's scheduled retention job registers
// under — used for the scheduler's own distributed-lock key and logs.
const JobName = "data-retention"

// Start registers RunOnce(ctx, false) on Runner's owner-specific daily cron
// tick (01:30 WIB plus that owner's fixed jitter minute) against sched.
// Returns an error if the owner has no entry in serviceJitterMinutes (a
// programming error — every real owner is listed above) or if the
// underlying cron spec fails to parse.
func (r *Runner) Start(sched *scheduler.Scheduler) error {
	jitter, ok := serviceJitterMinutes[r.owner]
	if !ok {
		return fmt.Errorf("retentionworker: owner %q has no registered schedule jitter", r.owner)
	}
	totalMinutes := 1*60 + 30 + jitter // 01:30 WIB base, plus this owner's fixed offset
	hour, minute := totalMinutes/60, totalMinutes%60
	spec := fmt.Sprintf("%d %d * * *", minute, hour)
	return sched.Cron(JobName, spec, func(ctx context.Context) error {
		report := r.RunOnce(ctx, false)
		for _, res := range report.Classes {
			if res.Err != nil {
				return res.Err // scheduler.Metrics records this run as failed; individual class errors are already logged in RunOnce.
			}
		}
		return nil
	}, scheduler.WithJobTimeout(10*time.Minute))
}
