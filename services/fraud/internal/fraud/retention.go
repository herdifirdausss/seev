package fraud

import (
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/herdifirdausss/seev/internal/platform/lifecycle/retention/worker"
	"github.com/herdifirdausss/seev/internal/platform/scheduling"
)

// StartRetentionRunner owns fraud's bounded screening-event retention job.
func (m *Module) StartRetentionRunner(redisClient *redis.Client, logger *slog.Logger) (func(), error) {
	var lock scheduler.LockProvider = scheduler.NewMemoryLock(2 * time.Minute)
	if redisClient != nil {
		lock = scheduler.NewRedisLock(redisClient, "fraud-retention")
	}
	runner, err := retentionworker.NewRunner("fraud", m.db, []retentionworker.Class{
		{Name: "fraud.screening_events", Action: "delete", FunctionName: "fn_retention_purge_screening_events"},
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
