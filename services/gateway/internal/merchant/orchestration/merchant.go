// Package merchant is the Gateway-owned Merchant/B2B API module
// (docs/roadmap/archive/57-c1-merchant-b2b-api.md, roadmap track C1). T2 wires
// persistence, configuration, and health/retention plumbing only — API-key
// auth (T3), quota/idempotency enforcement (T4), owner-service clients
// (T5/T6), the outbound webhook relay (T7), and HTTP handlers (T3+) are
// deliberately out of this file's scope; see services/gateway/internal/merchant/{auth,
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

	"github.com/herdifirdausss/seev/contracts/clients/ledger"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/lifecycle/retention/worker"
	"github.com/herdifirdausss/seev/internal/platform/scheduling"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/auth"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/lifecycle"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/repository"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/webhook"
)

// LedgerClient is the subset of contracts/clients/ledger.Client the T8 admin HTTP
// surface needs for account provisioning/inspection — a narrow structural
// interface (this codebase's own established pattern, e.g. services/gateway/internal/notification's
// Broker) so a nil client is a valid, explicit "account provisioning
// unavailable" state instead of forcing every caller of NewModule to dial
// Ledger first. currency is always the tenant's own DefaultCurrency.
type LedgerClient interface {
	ProvisionMerchant(ctx context.Context, tenantID uuid.UUID, currency string) (uuid.UUID, error)
	GetMerchantAccount(ctx context.Context, tenantID uuid.UUID) (ledgerclient.MerchantAccount, error)
}

// Module is the Merchant/B2B API's facade — constructed with Gateway's own
// database.DatabaseSQL, never a second connection or a different service's
// database (§3.1: "This module may use the Gateway database only for
// edge-owned state").
type Module struct {
	db   database.DatabaseSQL
	ring *cryptox.Ring

	Tenants     repository.TenantRepository
	APIKeys     repository.APIKeyRepository
	Quotas      repository.QuotaRepository
	Idempotency repository.IdempotencyRepository
	EventInbox  repository.EventInboxRepository
	Webhooks    repository.WebhookRepository
	Lifecycle   repository.LifecycleRepository
	Settings    repository.SettingsRepository

	// WebhookService is T7's tenant-facing endpoint management surface
	// (create/rotate/list/delete endpoints, list/get deliveries, replay) —
	// the counterpart of webhookRelay below, which is the dispatch side.
	WebhookService *webhook.Service
	webhookRelay   *webhook.RelayWorker
	webhookConsume *webhook.Consumer

	// LifecycleService is T8's maker-checker gate on live-mode activation
	// and tenant closure — see services/gateway/internal/merchant/lifecycle's own doc
	// comment.
	LifecycleService *lifecycle.Service

	// KeyService is T3's key create/rotate/revoke application service —
	// its own doc comment says "called by Admin BFF (T8), never directly
	// by a merchant," which is exactly what adminhttp.go now does.
	KeyService *auth.KeyService

	// Ledger is nil when NewModule's caller has no Ledger connection to
	// offer (e.g. a unit test constructing a Module without dialing
	// Ledger) — every adminhttp.go handler that needs it nil-checks
	// explicitly and returns 503 rather than panicking.
	Ledger LedgerClient

	// GlobalFlag is T9's own "global route-disable control" — an
	// incident-response kill switch for the entire merchant B2B API
	// surface, independent of any single tenant's own suspension. See
	// services/gateway/internal/merchant/auth.GlobalFlag's own doc comment for the
	// enforcement side (RequireB2BEnabled).
	GlobalFlag *auth.GlobalFlag
}

// NewModule panics if db, ring, or apiKeyPepper is nil/empty — matches this
// repository's own established convention (A8 T2.5b: construct now, not
// "construct then wire later"). ring is required unconditionally, same
// posture as every other encrypted-at-rest field in this codebase (webhook
// endpoint secrets, T7): there is no valid "cryptox unconfigured" mode to
// construct this module in. ledgerClient MAY be nil (see the Ledger field's
// own doc comment) — account-provisioning routes are the only thing that
// degrades, not construction itself.
func NewModule(db database.DatabaseSQL, ring *cryptox.Ring, apiKeyPepper string, ledgerClient LedgerClient) *Module {
	if db == nil {
		panic("merchant: NewModule requires a non-nil database")
	}
	if ring == nil {
		panic("merchant: NewModule requires a non-nil cryptox ring")
	}
	if apiKeyPepper == "" {
		panic("merchant: NewModule requires a non-empty apiKeyPepper")
	}
	webhooks := repository.NewWebhookRepository(db)
	tenants := repository.NewTenantRepository(db)
	lifecycleRepo := repository.NewLifecycleRepository(db)
	apiKeys := repository.NewAPIKeyRepository(db)
	settings := repository.NewSettingsRepository(db)
	return &Module{
		db:          db,
		ring:        ring,
		Tenants:     tenants,
		APIKeys:     apiKeys,
		Quotas:      repository.NewQuotaRepository(db),
		Idempotency: repository.NewIdempotencyRepository(db),
		EventInbox:  repository.NewEventInboxRepository(db),
		Webhooks:    webhooks,
		Lifecycle:   lifecycleRepo,
		Settings:    settings,

		WebhookService:   webhook.NewService(webhooks, ring),
		webhookRelay:     webhook.NewRelayWorker(webhooks, ring, nil),
		LifecycleService: lifecycle.NewService(lifecycleRepo, tenants),
		KeyService:       auth.NewKeyService(apiKeys, tenants, apiKeyPepper),
		Ledger:           ledgerClient,
		GlobalFlag:       auth.NewGlobalFlag(settings),
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
// services/gateway/internal/notification.Module.StartRetentionRunner (both are Gateway
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

// DefaultWebhookRelayInterval is how often StartWebhookRelay polls for due
// deliveries when the caller doesn't need a different cadence.
const DefaultWebhookRelayInterval = 10 * time.Second

// StartWebhookRelay launches T7's leasing delivery worker (services/gateway/internal/merchant/webhook.RelayWorker)
// on interval, matching StartRetentionRunner's own "stop func()" return
// shape. Safe to call from multiple gateway instances concurrently — the
// relay's own ClaimDue uses FOR UPDATE SKIP LOCKED, requiring no
// coordination beyond the database.
func (m *Module) StartWebhookRelay(ctx context.Context, interval time.Duration) (stop func()) {
	return m.webhookRelay.Start(ctx, interval)
}

// StartWebhookConsumer declares topology and launches T7's inbound
// RabbitMQ consumer (services/gateway/internal/merchant/webhook.Consumer), which is
// constructed lazily here rather than in NewModule since it needs a
// messaging.Broker that isn't available until services/gateway/cmd/gateway wires RabbitMQ —
// unlike webhookRelay, which only needs the ring already passed to
// NewModule. Call the returned stop func on shutdown.
func (m *Module) StartWebhookConsumer(ctx context.Context, broker webhook.Broker, logger *slog.Logger) (stop func(), err error) {
	m.webhookConsume = webhook.NewConsumer(m.Webhooks, m.Tenants, broker, logger)
	if err := m.webhookConsume.Start(ctx); err != nil {
		return nil, err
	}
	return m.webhookConsume.Stop, nil
}
