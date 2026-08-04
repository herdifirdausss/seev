package vendorboundary

import (
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/lifecycle/retention/worker"
	"github.com/herdifirdausss/seev/internal/platform/scheduling"
)

// StartRetentionRunner wires and starts vendor-service's
// docs/roadmap/archive/51-a8-data-lifecycle-privacy.md retention class
// (config/data-retention.yaml's vendor.callback_inbox) on its own dedicated
// scheduler — same construction as every other owner service's
// StartRetentionRunner (e.g. services/payin.Module.StartRetentionRunner).
// vendor-service never had this wired at all before: the policy declared
// vendor.callback_inbox/vendor.outbound_attempts from the start, but no
// retentionworker.Runner ever executed against them, discovered live via a
// load-test soak (vendor_callback_inbox grew unbounded, no purge ran).
// vendor.outbound_attempts is retain_permanent (no purge function exists
// for it, matching the payout.vendor_calls precedent) so only one class is
// registered here.
func StartRetentionRunner(db *database.DBSQL, redisClient *redis.Client, logger *slog.Logger) (stop func(), err error) {
	var lock scheduler.LockProvider
	if redisClient != nil {
		lock = scheduler.NewRedisLock(redisClient, "vendor-retention")
	} else {
		lock = scheduler.NewMemoryLock(2 * time.Minute)
	}

	runner, err := retentionworker.NewRunner("vendor", db, []retentionworker.Class{
		{Name: "vendor.callback_inbox", Action: "redact", FunctionName: "fn_retention_purge_callback_inbox_raw"},
	}, retentionworker.WithLogger(logger))
	if err != nil {
		return nil, err
	}

	sched := scheduler.NewScheduler(lock, scheduler.NewPrometheusMetrics(), scheduler.WithLocation(retentionworker.JakartaLocation))
	if err := runner.Start(sched); err != nil {
		sched.Stop()
		return nil, err
	}
	return sched.Stop, nil
}
