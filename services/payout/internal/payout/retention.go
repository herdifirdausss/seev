package payout

import (
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/herdifirdausss/seev/internal/platform/lifecycle/retention/worker"
	"github.com/herdifirdausss/seev/internal/platform/scheduling"
)

// StartRetentionRunner wires and starts payout's docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
// T2.6 retention class (config/data-retention.yaml) on its own dedicated
// scheduler — same construction as services/gateway/internal/notification.Module's own
// StartRetentionRunner. Only one class: payout.requests.destination_and_error
// was declared in T0's policy but never had a purge function until T2.6,
// since its redaction target (destination_ciphertext) didn't exist before
// T2.4.
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

	runner, err := retentionworker.NewRunner("payout", m.db, []retentionworker.Class{
		{Name: "payout.requests.destination_and_error", Action: "redact", FunctionName: "fn_retention_purge_requests_destination_and_error"},
		{Name: "payout.intake_commands", Action: "delete", FunctionName: "fn_retention_purge_intake_commands"},
		{Name: "payout.vendor_commands", Action: "delete", FunctionName: "fn_retention_purge_vendor_commands"},
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
