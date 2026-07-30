// Package model holds internal/merchant's own domain types (docs/roadmap/archive/57-c1-merchant-b2b-api.md
// §3.1's package boundary) — no repository, no HTTP, no DB driver import.
package model

import (
	"time"

	"github.com/google/uuid"
)

// Tenant is a merchant tenant (docs/roadmap/archive/57-c1-merchant-b2b-api.md §11.1).
// PrimaryAccountID is an application-level reference to a LedgerService
// account, never a cross-database foreign key (§11.1's own note).
//
// JSON tags (Plan 57 T10 found live): adminCreateTenant/adminGetTenant/
// adminListTenants/adminSuspendTenant return this struct directly, not a
// redacted DTO the way APIKey/WebhookEndpoint already do — without tags,
// encoding/json fell back to the bare Go field names (PascalCase),
// silently diverging from every other snake_case admin response in this
// API. Invisible to Go-internal tests (they unmarshal into this same
// untagged struct), only visible from a real external HTTP client.
type Tenant struct {
	ID               uuid.UUID  `json:"id"`
	PublicID         string     `json:"public_id"`
	ExternalCode     string     `json:"external_code"`
	Name             string     `json:"name"`
	Environment      string     `json:"environment"` // "sandbox" | "live"
	Status           string     `json:"status"`      // "draft" | "active" | "suspended" | "closed"
	DefaultCurrency  string     `json:"default_currency"`
	PrimaryAccountID *uuid.UUID `json:"primary_account_id,omitempty"`
	CreatedBy        string     `json:"created_by"`
	ActivatedBy      *string    `json:"activated_by,omitempty"`
	SuspendedBy      *string    `json:"suspended_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ActivatedAt      *time.Time `json:"activated_at,omitempty"`
	SuspendedAt      *time.Time `json:"suspended_at,omitempty"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
}

// APIKey is a merchant API key record (§11.2). The plaintext key is never
// stored — only SecretDigest (HMAC-SHA-256 with an application pepper, T3
// §8.2) and PublicPrefix (the non-secret lookup prefix, T3 §8.3).
// SecretDigest is `json:"-"` as defense-in-depth: adminhttp.go's own
// redactedKey DTO is the actual wire shape today, but a digest must never
// leave this process even if a future code path returns this struct raw.
type APIKey struct {
	ID           uuid.UUID  `json:"id"`
	PublicID     string     `json:"public_id"`
	TenantID     uuid.UUID  `json:"tenant_id"`
	PublicPrefix string     `json:"public_prefix"`
	SecretDigest []byte     `json:"-"`
	Environment  string     `json:"environment"` // "sandbox" | "live"
	Status       string     `json:"status"`      // "active" | "expired" | "revoked"
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	CreatedBy    string     `json:"created_by"`
	RevokedBy    *string    `json:"revoked_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	Scopes       []string   `json:"scopes"`
}

// QuotaPolicy is a per-tenant, per-quota-class rate limit (§11.4).
type QuotaPolicy struct {
	ID                uuid.UUID `json:"id"`
	TenantID          uuid.UUID `json:"tenant_id"`
	QuotaClass        string    `json:"quota_class"`
	RequestsPerMinute int       `json:"requests_per_minute"`
	Burst             int       `json:"burst"`
	IsEnabled         bool      `json:"is_enabled"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// IdempotencyRecord is a durable, tenant-scoped idempotent-write record
// (§11.5). The UNIQUE(tenant_id, operation_id, idempotency_key) constraint
// is what makes T4's "no tenant can collide with another tenant's
// idempotency key" a database guarantee, not just an application
// convention. RequestHash/ResponseBody/ResponseHeaders are `json:"-"` —
// this record is never returned over any HTTP surface, internal storage
// only.
type IdempotencyRecord struct {
	ID              uuid.UUID  `json:"id"`
	TenantID        uuid.UUID  `json:"tenant_id"`
	OperationID     string     `json:"operation_id"`
	IdempotencyKey  string     `json:"idempotency_key"`
	RequestHash     []byte     `json:"-"`
	DownstreamKey   string     `json:"downstream_key"`
	State           string     `json:"state"` // "processing" | "completed" | "failed"
	ResourceID      *string    `json:"resource_id,omitempty"`
	HTTPStatus      *int       `json:"http_status,omitempty"`
	ResponseBody    []byte     `json:"-"` // JSON
	ResponseHeaders []byte     `json:"-"` // JSON
	ErrorCode       *string    `json:"error_code,omitempty"`
	LeaseOwner      *string    `json:"lease_owner,omitempty"`
	LeaseExpiresAt  *time.Time `json:"lease_expires_at,omitempty"`
	ExpiresAt       time.Time  `json:"expires_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// InboxEvent deduplicates an internal AMQP event before it becomes an
// external webhook event (§11.6). Never returned over any HTTP surface.
type InboxEvent struct {
	EventID         uuid.UUID  `json:"event_id"`
	EventType       string     `json:"event_type"`
	Source          string     `json:"source"`
	PayloadHash     []byte     `json:"-"`
	ReceivedAt      time.Time  `json:"received_at"`
	ProcessedAt     *time.Time `json:"processed_at,omitempty"`
	ProcessingError *string    `json:"processing_error,omitempty"`
}

// WebhookEndpoint is a merchant-configured outbound webhook destination
// (§11.7). SecretCiphertext is sealed via pkg/cryptox (T7); plaintext is
// shown only at creation and rotation. SecretCiphertext is `json:"-"` as
// defense-in-depth — adminhttp.go's redactedWebhookEndpoint DTO is the
// actual wire shape.
type WebhookEndpoint struct {
	ID               uuid.UUID `json:"id"`
	PublicID         string    `json:"public_id"`
	TenantID         uuid.UUID `json:"tenant_id"`
	URL              string    `json:"url"`
	Status           string    `json:"status"` // "enabled" | "disabled"
	SecretCiphertext []byte    `json:"-"`
	SecretVersion    int       `json:"secret_version"`
	SubscribedEvents []string  `json:"subscribed_events"`
	// Environment ("sandbox" | "live", Plan 57 T7) is fixed at creation
	// (mirrors merchant_api_keys.environment) and gates whether the relay's
	// SSRF check runs at dispatch time — sandbox endpoints may legitimately
	// target a local receiver (docs/reference/c1-b2b-design.md §4).
	Environment string     `json:"environment"`
	Description *string    `json:"description,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DisabledAt  *time.Time `json:"disabled_at,omitempty"`
}

// WebhookEvent is an immutable external event, persisted once (§11.8).
// PayloadBytes is the exact serialized body used for signing and retry —
// it must never be re-serialized from Payload for a redelivery.
type WebhookEvent struct {
	ID            uuid.UUID `json:"id"`
	PublicID      string    `json:"public_id"`
	TenantID      uuid.UUID `json:"tenant_id"`
	EventType     string    `json:"event_type"`
	SchemaVersion int       `json:"schema_version"`
	Livemode      bool      `json:"livemode"`
	Payload       []byte    `json:"payload,omitempty"` // JSON
	PayloadBytes  []byte    `json:"-"`
	SourceEventID uuid.UUID `json:"source_event_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// WebhookDelivery tracks one endpoint's delivery attempts for one event
// (§11.9). UNIQUE(endpoint_id, event_id) bounds the AUTOMATIC delivery
// path to at most one row per (endpoint, event); a replay (T7) inserts a
// new delivery row with the same EventID but is not subject to this
// constraint at the application layer's replay call site.
type WebhookDelivery struct {
	ID                 uuid.UUID  `json:"id"`
	PublicID           string     `json:"public_id"`
	TenantID           uuid.UUID  `json:"tenant_id"`
	EndpointID         uuid.UUID  `json:"endpoint_id"`
	EventID            uuid.UUID  `json:"event_id"`
	ReplayOfDeliveryID *uuid.UUID `json:"replay_of_delivery_id,omitempty"` // nil for the automatic delivery; set for every replay row
	Status             string     `json:"status"`                          // "pending" | "delivered" | "failed" | "dead"
	AttemptCount       int        `json:"attempt_count"`
	NextAttemptAt      *time.Time `json:"next_attempt_at,omitempty"`
	LeaseOwner         *string    `json:"lease_owner,omitempty"`
	LeaseExpiresAt     *time.Time `json:"lease_expires_at,omitempty"`
	LastHTTPStatus     *int       `json:"last_http_status,omitempty"`
	LastErrorCode      *string    `json:"last_error_code,omitempty"`
	FirstAttemptAt     *time.Time `json:"first_attempt_at,omitempty"`
	DeliveredAt        *time.Time `json:"delivered_at,omitempty"`
	DeadAt             *time.Time `json:"dead_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// TenantLifecycleRequest is a maker-checker record gating a sensitive
// tenant status transition (Plan 57 T8 §16.3: "live-mode activation:
// checker", "tenant closure: checker") — mirrors
// internal/auth.OperatorOffboardingRequest's own shape, generalized to
// two Action kinds instead of one hardcoded operation. RequestedBy/
// ApprovedBy are OPERATOR identities (admin email or user id), never the
// tenant's own data.
type TenantLifecycleRequest struct {
	ID          uuid.UUID  `json:"id"`
	TenantID    uuid.UUID  `json:"tenant_id"`
	Action      string     `json:"action"` // "activate" | "close"
	RequestedBy string     `json:"requested_by"`
	ApprovedBy  string     `json:"approved_by"`
	Reason      string     `json:"reason"`
	Status      string     `json:"status"` // "pending" | "approved" | "rejected"
	CreatedAt   time.Time  `json:"created_at"`
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
}

// WebhookAttempt is one append-only attempt record (§11.10). Do not
// persist a sensitive response body — ResponseExcerpt must already be
// truncated/sanitized by the caller before Insert.
type WebhookAttempt struct {
	ID              uuid.UUID `json:"id"`
	DeliveryID      uuid.UUID `json:"delivery_id"`
	AttemptNumber   int       `json:"attempt_number"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	HTTPStatus      *int      `json:"http_status,omitempty"`
	DurationMS      int       `json:"duration_ms"`
	ErrorCode       *string   `json:"error_code,omitempty"`
	ResponseExcerpt *string   `json:"response_excerpt,omitempty"`
}
