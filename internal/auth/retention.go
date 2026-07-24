package auth

import (
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/herdifirdausss/seev/pkg/retentionworker"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// StartRetentionRunner wires and starts auth's docs/roadmap/active/51-a8-data-lifecycle-privacy.md
// T1 retention classes (config/data-retention.yaml) on their own dedicated
// scheduler — same lock-construction convention as NewKYCApplyRetryJob/
// NewKYCRescreenJob above, but this job type needs a *scheduler.Scheduler
// (pkg/retentionworker.Runner.Start), not a bare context, so it owns and
// returns that scheduler's Stop func directly rather than exposing the
// scheduler itself.
func (m *Module) StartRetentionRunner(redisClient *redis.Client, logger *slog.Logger) (stop func(), err error) {
	var lock scheduler.LockProvider
	if redisClient != nil {
		instanceID, hostErr := os.Hostname()
		if hostErr != nil || instanceID == "" {
			instanceID = uuid.NewString()
		}
		lock = scheduler.NewRedisLock(redisClient, instanceID)
	} else {
		lock = scheduler.NewMemoryLock(2 * time.Minute)
	}

	runner, err := retentionworker.NewRunner("auth", m.db, []retentionworker.Class{
		{Name: "auth.refresh_tokens", Action: "delete", FunctionName: "fn_retention_purge_refresh_tokens"},
	})
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
