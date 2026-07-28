// Package model holds internal/merchant's own domain types (docs/roadmap/active/57-c1-merchant-b2b-api.md
// §3.1's package boundary) — no repository, no HTTP, no DB driver import.
package model

import (
	"time"

	"github.com/google/uuid"
)

// Tenant is a merchant tenant (docs/roadmap/active/57-c1-merchant-b2b-api.md §11.1).
// PrimaryAccountID is an application-level reference to a LedgerService
// account, never a cross-database foreign key (§11.1's own note).
type Tenant struct {
	ID                uuid.UUID
	PublicID          string
	ExternalCode      string
	Name              string
	Environment       string // "sandbox" | "live"
	Status            string // "draft" | "active" | "suspended" | "closed"
	DefaultCurrency   string
	PrimaryAccountID  *uuid.UUID
	CreatedBy         string
	ActivatedBy       *string
	SuspendedBy       *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ActivatedAt       *time.Time
	SuspendedAt       *time.Time
	ClosedAt          *time.Time
}

// APIKey is a merchant API key record (§11.2). The plaintext key is never
// stored — only SecretDigest (HMAC-SHA-256 with an application pepper, T3
// §8.2) and PublicPrefix (the non-secret lookup prefix, T3 §8.3).
type APIKey struct {
	ID           uuid.UUID
	PublicID     string
	TenantID     uuid.UUID
	PublicPrefix string
	SecretDigest []byte
	Environment  string // "sandbox" | "live"
	Status       string // "active" | "expired" | "revoked"
	ExpiresAt    *time.Time
	LastUsedAt   *time.Time
	CreatedBy    string
	RevokedBy    *string
	CreatedAt    time.Time
	RevokedAt    *time.Time
	Scopes       []string
}

// QuotaPolicy is a per-tenant, per-quota-class rate limit (§11.4).
type QuotaPolicy struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	QuotaClass        string
	RequestsPerMinute int
	Burst             int
	IsEnabled         bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// IdempotencyRecord is a durable, tenant-scoped idempotent-write record
// (§11.5). The UNIQUE(tenant_id, operation_id, idempotency_key) constraint
// is what makes T4's "no tenant can collide with another tenant's
// idempotency key" a database guarantee, not just an application
// convention.
type IdempotencyRecord struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	OperationID      string
	IdempotencyKey   string
	RequestHash      []byte
	DownstreamKey    string
	State            string // "processing" | "completed" | "failed"
	ResourceID       *string
	HTTPStatus       *int
	ResponseBody     []byte // JSON
	ResponseHeaders  []byte // JSON
	ErrorCode        *string
	LeaseOwner       *string
	LeaseExpiresAt   *time.Time
	ExpiresAt        time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// InboxEvent deduplicates an internal AMQP event before it becomes an
// external webhook event (§11.6).
type InboxEvent struct {
	EventID         uuid.UUID
	EventType       string
	Source          string
	PayloadHash     []byte
	ReceivedAt      time.Time
	ProcessedAt     *time.Time
	ProcessingError *string
}

// WebhookEndpoint is a merchant-configured outbound webhook destination
// (§11.7). SecretCiphertext is sealed via pkg/cryptox (T7); plaintext is
// shown only at creation and rotation.
type WebhookEndpoint struct {
	ID                uuid.UUID
	PublicID          string
	TenantID          uuid.UUID
	URL               string
	Status            string // "enabled" | "disabled"
	SecretCiphertext  []byte
	SecretVersion     int
	SubscribedEvents  []string
	Description       *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DisabledAt        *time.Time
}

// WebhookEvent is an immutable external event, persisted once (§11.8).
// PayloadBytes is the exact serialized body used for signing and retry —
// it must never be re-serialized from Payload for a redelivery.
type WebhookEvent struct {
	ID             uuid.UUID
	PublicID       string
	TenantID       uuid.UUID
	EventType      string
	SchemaVersion  int
	Livemode       bool
	Payload        []byte // JSON
	PayloadBytes   []byte
	SourceEventID  uuid.UUID
	CreatedAt      time.Time
}

// WebhookDelivery tracks one endpoint's delivery attempts for one event
// (§11.9). UNIQUE(endpoint_id, event_id) bounds the AUTOMATIC delivery
// path to at most one row per (endpoint, event); a replay (T7) inserts a
// new delivery row with the same EventID but is not subject to this
// constraint at the application layer's replay call site.
type WebhookDelivery struct {
	ID                 uuid.UUID
	PublicID           string
	TenantID           uuid.UUID
	EndpointID         uuid.UUID
	EventID            uuid.UUID
	ReplayOfDeliveryID *uuid.UUID // nil for the automatic delivery; set for every replay row
	Status             string     // "pending" | "delivered" | "failed" | "dead"
	AttemptCount    int
	NextAttemptAt   *time.Time
	LeaseOwner      *string
	LeaseExpiresAt  *time.Time
	LastHTTPStatus  *int
	LastErrorCode   *string
	FirstAttemptAt  *time.Time
	DeliveredAt     *time.Time
	DeadAt          *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// WebhookAttempt is one append-only attempt record (§11.10). Do not
// persist a sensitive response body — ResponseExcerpt must already be
// truncated/sanitized by the caller before Insert.
type WebhookAttempt struct {
	ID               uuid.UUID
	DeliveryID       uuid.UUID
	AttemptNumber    int
	StartedAt        time.Time
	FinishedAt       time.Time
	HTTPStatus       *int
	DurationMS       int
	ErrorCode        *string
	ResponseExcerpt  *string
}
