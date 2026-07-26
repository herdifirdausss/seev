package payin

import (
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/herdifirdausss/seev/pkg/retentionworker"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// StartRetentionRunner wires and starts payin's docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
// T2.6 retention class (config/data-retention.yaml) on its own dedicated
// scheduler — same construction as internal/notify.Module's own
// StartRetentionRunner. Only one class: payin.webhook_events.raw was
// declared in T0's policy but never had a purge function until T2.6, since
// its redaction target (raw_ciphertext) didn't exist before T2.4.
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

	runner, err := retentionworker.NewRunner("payin", m.db, []retentionworker.Class{
		{Name: "payin.webhook_events.raw", Action: "redact", FunctionName: "fn_retention_purge_webhook_events_raw"},
		{Name: "payin.intake_commands", Action: "delete", FunctionName: "fn_retention_purge_intake_commands"},
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
