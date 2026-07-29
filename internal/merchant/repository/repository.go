// Package repository is internal/merchant's Gateway-owned persistence layer
// (docs/roadmap/active/57-c1-merchant-b2b-api.md §3.1). Every method for a
// tenant-owned resource takes tenantID explicitly (§7.3's code-review rule:
// GetTransaction(ctx, transactionID) is rejected, GetTransaction(ctx,
// tenantID, transactionID) is required) — this is what makes a missing
// WHERE tenant_id = $N a compile-time-visible omission in every call site,
// not just a runtime bug waiting to leak across tenants.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

// ErrNotFound is returned when a tenant-scoped lookup matches no row —
// deliberately identical whether the row never existed or belongs to a
// different tenant (§7.3's own required behavior: no existence leak).
var ErrNotFound = errors.New("merchant: not found")

// TenantRepository persists merchant_tenants. Unlike every other
// repository in this package, TenantRepository's own lookups are not
// tenant-scoped-by-a-DIFFERENT-tenant — a tenant IS the scoping identity,
// not a resource owned by one.
type TenantRepository interface {
	Create(ctx context.Context, t model.Tenant) error
	GetByID(ctx context.Context, id uuid.UUID) (model.Tenant, error)
	GetByPublicID(ctx context.Context, publicID string) (model.Tenant, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status, actor string) error
	SetPrimaryAccount(ctx context.Context, id uuid.UUID, accountID uuid.UUID) error
}

// APIKeyRepository persists merchant_api_keys and merchant_api_key_scopes.
type APIKeyRepository interface {
	Create(ctx context.Context, k model.APIKey) error
	// GetActiveByPrefix is T3 §8.3 step 3 — "fetch the active candidate
	// record by unique prefix." Intentionally NOT tenant-scoped: at
	// authentication time the caller does not yet know the tenant: the key
	// itself resolves it.
	GetActiveByPrefix(ctx context.Context, prefix string) (model.APIKey, error)
	ListByTenant(ctx context.Context, tenantID uuid.UUID) ([]model.APIKey, error)
	Revoke(ctx context.Context, tenantID, keyID uuid.UUID, actor string) error
	TouchLastUsed(ctx context.Context, keyID uuid.UUID) error
}

// QuotaRepository persists merchant_quota_policies.
type QuotaRepository interface {
	Upsert(ctx context.Context, p model.QuotaPolicy) error
	GetByTenantAndClass(ctx context.Context, tenantID uuid.UUID, quotaClass string) (model.QuotaPolicy, error)
}

// IdempotencyRepository persists merchant_idempotency_records. Claim/
// Complete/Fail implement T4's claim-lease-complete lifecycle; the actual
// enforcement logic (canonical hashing, downstream key derivation, replay)
// lives in internal/merchant/idempotency (T4), not here — this is
// persistence only.
type IdempotencyRepository interface {
	Claim(ctx context.Context, r model.IdempotencyRecord) (claimed bool, existing model.IdempotencyRecord, err error)
	Complete(ctx context.Context, tenantID uuid.UUID, id uuid.UUID, httpStatus int, responseBody, responseHeaders []byte, resourceID *string) error
	Fail(ctx context.Context, tenantID uuid.UUID, id uuid.UUID, errorCode string) error
	GetByKey(ctx context.Context, tenantID uuid.UUID, operationID, idempotencyKey string) (model.IdempotencyRecord, error)
	// TakeoverExpiredLease is T4's "recovery query for interrupted
	// processing records" — a conditional claim: succeeds ONLY if the
	// record is still 'processing' with an ALREADY-EXPIRED lease (the
	// original claimant crashed mid-request), atomically reassigning the
	// lease to newLeaseOwner. Two concurrent retries racing this method
	// can only have one succeed, by the same WHERE-clause-as-compare-and-swap
	// pattern Claim's own ON CONFLICT DO NOTHING relies on.
	TakeoverExpiredLease(ctx context.Context, tenantID, id uuid.UUID, newLeaseOwner string, newLeaseExpiresAt time.Time) (took bool, err error)
	// ReclaimFailed atomically transitions a 'failed' record back to
	// 'processing' under a new lease for a fresh retry attempt — the same
	// compare-and-swap shape as TakeoverExpiredLease, guarding against two
	// concurrent retries of a previously-failed request both proceeding to
	// re-run the operation.
	ReclaimFailed(ctx context.Context, tenantID, id uuid.UUID, newLeaseOwner string, newLeaseExpiresAt time.Time) (reclaimed bool, err error)
}

// EventInboxRepository persists merchant_event_inbox.
type EventInboxRepository interface {
	// TryInsert dedups on the PRIMARY KEY (event_id) — inserted=false means
	// this internal event was already seen (RabbitMQ at-least-once
	// redelivery), which the caller must treat identically to "already
	// processed", not an error.
	TryInsert(ctx context.Context, e model.InboxEvent) (inserted bool, err error)
	MarkProcessed(ctx context.Context, eventID uuid.UUID) error
	MarkFailed(ctx context.Context, eventID uuid.UUID, errMsg string) error
}

// WebhookRepository persists merchant_webhook_endpoints,
// merchant_webhook_events, merchant_webhook_deliveries, and
// merchant_webhook_attempts.
type WebhookRepository interface {
	CreateEndpoint(ctx context.Context, e model.WebhookEndpoint) error
	GetEndpoint(ctx context.Context, tenantID, endpointID uuid.UUID) (model.WebhookEndpoint, error)
	ListEndpoints(ctx context.Context, tenantID uuid.UUID) ([]model.WebhookEndpoint, error)
	UpdateEndpoint(ctx context.Context, e model.WebhookEndpoint) error
	DeleteEndpoint(ctx context.Context, tenantID, endpointID uuid.UUID) error
	DisableEndpoint(ctx context.Context, endpointID uuid.UUID) error

	CreateEvent(ctx context.Context, e model.WebhookEvent) error
	GetEventBySource(ctx context.Context, tenantID, sourceEventID uuid.UUID, eventType string) (model.WebhookEvent, bool, error)
	// GetEventByID is the relay worker's own lookup (relay.go) — a claimed
	// WebhookDelivery only carries EventID, not the event body, so dispatch
	// must fetch the immutable event row (PayloadBytes) separately before
	// it can sign and send.
	GetEventByID(ctx context.Context, eventID uuid.UUID) (model.WebhookEvent, error)

	// CreateDelivery dedups the AUTOMATIC path on UNIQUE(endpoint_id,
	// event_id) — created=false means this (endpoint, event) pair already
	// has a delivery row (T7 acceptance: "one endpoint receives at most one
	// automatic delivery record per event").
	CreateDelivery(ctx context.Context, d model.WebhookDelivery) (created bool, err error)
	// CreateReplayDelivery always sets d.ReplayOfDeliveryID (non-nil) —
	// callers replay an EXISTING delivery, never create a bare replay with
	// no lineage.
	CreateReplayDelivery(ctx context.Context, d model.WebhookDelivery) error
	GetDelivery(ctx context.Context, tenantID, deliveryID uuid.UUID) (model.WebhookDelivery, error)
	ListDeliveries(ctx context.Context, tenantID uuid.UUID, limit int) ([]model.WebhookDelivery, error)
	ListDue(ctx context.Context, limit int) ([]model.WebhookDelivery, error)
	// ClaimDue is the T7 relay worker's own atomic claim — a due delivery
	// (status pending/failed, next_attempt_at due) whose lease is either
	// unheld or EXPIRED is atomically assigned to leaseOwner via
	// `FOR UPDATE SKIP LOCKED` (same shape as T4's own
	// IdempotencyRepository.TakeoverExpiredLease). This is what makes
	// "worker restart recovers expired leases" true: a crashed worker's
	// dangling lease is reclaimed by the next poll once it expires, no
	// separate recovery pass needed.
	ClaimDue(ctx context.Context, limit int, leaseOwner string, leaseExpiresAt time.Time) ([]model.WebhookDelivery, error)
	MarkDelivered(ctx context.Context, deliveryID uuid.UUID, httpStatus int) error
	MarkFailedAttempt(ctx context.Context, deliveryID uuid.UUID, errorCode string, httpStatus *int, nextAttemptAt any) error
	MarkDead(ctx context.Context, deliveryID uuid.UUID) error

	RecordAttempt(ctx context.Context, a model.WebhookAttempt) error
}

// LifecycleRepository persists merchant_tenant_lifecycle_requests (Plan 57
// T8's maker-checker gate on live-mode activation and tenant closure).
type LifecycleRepository interface {
	// Create is the "maker" half's own insert — ON CONFLICT DO NOTHING
	// against the partial unique index on (tenant_id, action) WHERE
	// status = 'pending', so a duplicate propose for the same pending
	// action is idempotent rather than an error: created=false means an
	// existing pending request for this (tenant, action) was returned
	// instead.
	Create(ctx context.Context, req model.TenantLifecycleRequest) (created bool, existing model.TenantLifecycleRequest, err error)
	GetByID(ctx context.Context, id uuid.UUID) (model.TenantLifecycleRequest, error)
	GetPending(ctx context.Context, tenantID uuid.UUID, action string) (model.TenantLifecycleRequest, bool, error)
	List(ctx context.Context, tenantID uuid.UUID, status string, limit int) ([]model.TenantLifecycleRequest, error)
	// Decide atomically transitions a pending request to approved/rejected
	// — matched=false means it was no longer 'pending' (already decided by
	// a concurrent call), the same compare-and-swap shape as
	// IdempotencyRepository.TakeoverExpiredLease.
	Decide(ctx context.Context, id uuid.UUID, status, approvedBy string) (matched bool, err error)
}

// NewTenantRepository panics on a nil db — every repository in this
// package requires a real, non-nil connection at construction (matches
// this repository's own established A8 T2.5b convention: construct now,
// not "construct then wire later").
func NewTenantRepository(db database.DatabaseSQL) TenantRepository {
	if db == nil {
		panic("merchant: NewTenantRepository requires a non-nil database")
	}
	return &tenantRepository{db: db}
}

func NewAPIKeyRepository(db database.DatabaseSQL) APIKeyRepository {
	if db == nil {
		panic("merchant: NewAPIKeyRepository requires a non-nil database")
	}
	return &apiKeyRepository{db: db}
}

func NewQuotaRepository(db database.DatabaseSQL) QuotaRepository {
	if db == nil {
		panic("merchant: NewQuotaRepository requires a non-nil database")
	}
	return &quotaRepository{db: db}
}

func NewIdempotencyRepository(db database.DatabaseSQL) IdempotencyRepository {
	if db == nil {
		panic("merchant: NewIdempotencyRepository requires a non-nil database")
	}
	return &idempotencyRepository{db: db}
}

func NewEventInboxRepository(db database.DatabaseSQL) EventInboxRepository {
	if db == nil {
		panic("merchant: NewEventInboxRepository requires a non-nil database")
	}
	return &eventInboxRepository{db: db}
}

func NewWebhookRepository(db database.DatabaseSQL) WebhookRepository {
	if db == nil {
		panic("merchant: NewWebhookRepository requires a non-nil database")
	}
	return &webhookRepository{db: db}
}

func NewLifecycleRepository(db database.DatabaseSQL) LifecycleRepository {
	if db == nil {
		panic("merchant: NewLifecycleRepository requires a non-nil database")
	}
	return &lifecycleRepository{db: db}
}
