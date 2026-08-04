package notify

import (
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/herdifirdausss/seev/internal/platform/lifecycle/retention/worker"
	"github.com/herdifirdausss/seev/internal/platform/scheduling"
)

// StartRetentionRunner wires and starts gateway's docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
// T1.7 retention classes (config/data-retention.yaml) on their own dedicated
// scheduler — same construction as services/auth.Module's own
// StartRetentionRunner. Secret recipient material is redacted before the
// longer-lived delivery evidence is deleted.
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

	runner, err := retentionworker.NewRunner("gateway", m.db, []retentionworker.Class{
		{Name: "gateway.notifications.read", Action: "delete", FunctionName: "fn_retention_purge_notifications_read"},
		{Name: "gateway.notifications.any", Action: "delete", FunctionName: "fn_retention_purge_notifications_any"},
		{Name: "gateway.notifications.event_inbox", Action: "delete", FunctionName: "fn_retention_purge_notification_event_inbox_processed"},
		{Name: "gateway.notifications.event_inbox_failed", Action: "delete", FunctionName: "fn_retention_purge_notification_event_inbox_failed"},
		{Name: "gateway.notifications.delivery_attempts", Action: "delete", FunctionName: "fn_retention_purge_notification_delivery_attempts"},
		{Name: "gateway.notifications.recipient_ciphertext", Action: "redact", FunctionName: "fn_retention_redact_notification_recipient"},
		{Name: "gateway.notifications.device_tokens", Action: "redact", FunctionName: "fn_retention_redact_notification_device_tokens"},
		{Name: "gateway.notifications.deliveries", Action: "delete", FunctionName: "fn_retention_purge_notification_deliveries"},
		{Name: "gateway.notifications.digest_items", Action: "delete", FunctionName: "fn_retention_purge_notification_digest_items"},
		{Name: "gateway.notifications.digest_windows", Action: "delete", FunctionName: "fn_retention_purge_notification_digest_windows"},
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
