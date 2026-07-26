package assurance

import (
	"log/slog"
	"time"

	"github.com/herdifirdausss/seev/pkg/retentionworker"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// StartRetentionRunner wires and starts assurance's docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
// T1.7 retention classes (config/data-retention.yaml) on their own
// dedicated scheduler. Unlike internal/auth/internal/notify's own
// StartRetentionRunner, this always uses an in-memory lock: this service
// has no Redis dependency anywhere else in its wiring (cmd/assurance-service/main.go),
// so a distributed lock would be new infrastructure this task doesn't need
// to introduce — matches assurance's existing single-instance assumption.
func (m *Module) StartRetentionRunner(logger *slog.Logger) (stop func(), err error) {
	runner, err := retentionworker.NewRunner("assurance", m.db, []retentionworker.Class{
		{Name: "assurance.runs.succeeded", Action: "delete", FunctionName: "fn_retention_purge_runs_succeeded"},
		{Name: "assurance.runs.failed", Action: "delete", FunctionName: "fn_retention_purge_runs_failed"},
		{Name: "assurance.findings.resolved", Action: "delete", FunctionName: "fn_retention_purge_findings_resolved"},
		{Name: "assurance.alert_deliveries", Action: "delete", FunctionName: "fn_retention_purge_alert_deliveries"},
		{Name: "assurance.intake_commands", Action: "delete", FunctionName: "fn_retention_purge_intake_commands"},
	}, retentionworker.WithLogger(logger))
	if err != nil {
		return nil, err
	}

	sched := scheduler.NewScheduler(scheduler.NewMemoryLock(2*time.Minute), scheduler.NewPrometheusMetrics(), scheduler.WithLocation(retentionworker.JakartaLocation))
	if err := runner.Start(sched); err != nil {
		sched.Stop()
		return nil, err
	}
	return sched.Stop, nil
}
