// Package merchant is the Gateway-owned Merchant/B2B API module
// (docs/roadmap/active/57-c1-merchant-b2b-api.md, roadmap track C1). T2 wires
// persistence, configuration, and health/retention plumbing only — API-key
// auth (T3), quota/idempotency enforcement (T4), owner-service clients
// (T5/T6), the outbound webhook relay (T7), and HTTP handlers (T3+) are
// deliberately out of this file's scope; see internal/merchant/{auth,
// quota, idempotency, webhook, client, api} for those subpackages as they
// land task by task.
package merchant

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/retentionworker"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// Module is the Merchant/B2B API's facade — constructed with Gateway's own
// database.DatabaseSQL, never a second connection or a different service's
// database (§3.1: "This module may use the Gateway database only for
// edge-owned state").
type Module struct {
	db database.DatabaseSQL

	Tenants      repository.TenantRepository
	APIKeys      repository.APIKeyRepository
	Quotas       repository.QuotaRepository
	Idempotency  repository.IdempotencyRepository
	EventInbox   repository.EventInboxRepository
	Webhooks     repository.WebhookRepository
}

// NewModule panics if db is nil — matches this repository's own
// established convention (A8 T2.5b: construct now, not "construct then
// wire later").
func NewModule(db database.DatabaseSQL) *Module {
	if db == nil {
		panic("merchant: NewModule requires a non-nil database")
	}
	return &Module{
		db:          db,
		Tenants:     repository.NewTenantRepository(db),
		APIKeys:     repository.NewAPIKeyRepository(db),
		Quotas:      repository.NewQuotaRepository(db),
		Idempotency: repository.NewIdempotencyRepository(db),
		EventInbox:  repository.NewEventInboxRepository(db),
		Webhooks:    repository.NewWebhookRepository(db),
	}
}

// Healthy reports whether the module's own database dependency is
// reachable — T2's "module health/readiness checks where required".
func (m *Module) Healthy(ctx context.Context) error {
	if err := m.db.HealthCheck(ctx); err != nil {
		return fmt.Errorf("merchant: health check: %w", err)
	}
	return nil
}

// StartRetentionRunner wires T2's "retention jobs for idempotency and
// delivery evidence" (config/data-retention.yaml's gateway.merchant.*
// classes) on their own dedicated scheduler — same construction as
// internal/notify.Module.StartRetentionRunner (both are Gateway
// submodules, both register under retentionworker.NewRunner("gateway",
// ...), since retention audit rows are already namespaced by `class`, not
// by which Gateway submodule wrote them).
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
		{Name: "gateway.merchant.idempotency_records", Action: "delete", FunctionName: "fn_retention_purge_merchant_idempotency_records"},
		{Name: "gateway.merchant.api_keys_revoked", Action: "delete", FunctionName: "fn_retention_purge_merchant_api_keys_revoked"},
		{Name: "gateway.merchant.event_inbox", Action: "delete", FunctionName: "fn_retention_purge_merchant_event_inbox"},
		{Name: "gateway.merchant.webhook_deliveries", Action: "delete", FunctionName: "fn_retention_purge_merchant_webhook_deliveries"},
		{Name: "gateway.merchant.webhook_events", Action: "delete", FunctionName: "fn_retention_purge_merchant_webhook_events"},
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
