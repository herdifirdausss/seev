package model

import (
	"time"

	"github.com/google/uuid"
)

// Notification kinds are stable semantic identifiers. They deliberately do
// not mirror a source event's transaction_type: one Ledger fact can produce
// different user-facing notifications for different recipients.
const (
	KindTransferSent         = "money.transfer.sent"
	KindTransferReceived     = "money.transfer.received"
	KindTopupSucceeded       = "money.topup.succeeded"
	KindPayoutSucceeded      = "money.payout.succeeded"
	KindPayoutCancelled      = "money.payout.cancelled"
	KindDailyDigest          = "system.daily_digest"
	CategoryMoneyMovement    = "money_movement"
	CategoryAccount          = "account"
	CategorySecurity         = "security"
	CategoryCompliance       = "compliance"
	CategorySystem           = "system"
	PriorityCritical         = "critical"
	PriorityHigh             = "high"
	PriorityNormal           = "normal"
	PriorityLow              = "low"
	RequirementMandatory     = "mandatory"
	RequirementTransactional = "transactional"
	RequirementOptional      = "optional"
	ChannelInApp             = "in_app"
	ChannelEmail             = "email"
	ChannelPush              = "push"
	ModeImmediate            = "immediate"
	ModeDailyDigest          = "daily_digest"
	ModeDisabled             = "disabled"
	DeliveryPendingRecipient = "pending_recipient"
	DeliveryScheduled        = "scheduled"
	DeliveryProcessing       = "processing"
	DeliveryRetryWait        = "retry_wait"
	DeliveryDelivered        = "delivered"
	DeliverySuppressed       = "suppressed"
	DeliveryBlocked          = "blocked"
	DeliveryDead             = "dead"
	DeliveryCancelled        = "cancelled"
)

// Notification is one row of notif_notifications (docs/roadmap/archive/25 Task T4) —
// one user's copy of a ledger TransactionPosted event. A two-party
// transaction (transfer_p2p) produces two independent rows, one per
// (EventID, UserID) — UNIQUE(event_id, user_id) is the at-least-once
// dedup guard against RabbitMQ redelivery of the same outbox event.
type Notification struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	EventID   uuid.UUID
	Type      string // ledger transaction_type: money_in, transfer_p2p, withdraw_settle, withdraw_cancel
	Title     string
	Body      string
	Payload   []byte // raw JSON — the decoded events.TransactionPosted, forensic/debug only
	ReadAt    *time.Time
	CreatedAt time.Time

	// C3 fields are additive. Type and Payload remain populated/understood for
	// legacy rows, while new code uses Kind and Context instead of raw event
	// payloads.
	EventType         string
	SourceService     string
	Kind              string
	Category          string
	Priority          string
	Requirement       string
	Locale            string
	TemplateVersionID *uuid.UUID
	DeepLink          string
	Context           []byte
	ContentHash       []byte
	ExpiresAt         *time.Time
	UpdatedAt         time.Time
}

// MoneyContext is the only amount representation exposed to templates. Minor
// is retained as a string to avoid floating-point conversion in rendering.
type MoneyContext struct {
	Minor    string `json:"minor"`
	Currency string `json:"currency"`
	Display  string `json:"display"`
}

type TransactionContext struct {
	ID       string    `json:"id"`
	PostedAt time.Time `json:"posted_at"`
}

type ActionContext struct {
	DeepLink string `json:"deep_link"`
}

// RenderContext is the bounded, typed context accepted by the renderer. Raw
// AMQP payloads, credentials, arbitrary URLs, and provider fields do not have
// a representation here by design.
type RenderContext struct {
	NotificationID string             `json:"notification_id"`
	Amount         MoneyContext       `json:"amount"`
	Transaction    TransactionContext `json:"transaction"`
	Action         ActionContext      `json:"action"`
}

type EventInbox struct {
	ID            uuid.UUID
	SourceService string
	EventID       uuid.UUID
	EventType     string
	SchemaVersion int
	PayloadHash   []byte
	Status        string
	ErrorCode     string
	ReceivedAt    time.Time
	ProcessedAt   *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Delivery is a durable per-channel plan. Rendered fields are immutable after
// planning so retries cannot observe a later template version.
type Delivery struct {
	ID                   uuid.UUID
	NotificationID       *uuid.UUID
	DigestWindowID       *uuid.UUID
	UserID               uuid.UUID
	Channel              string
	EndpointID           *uuid.UUID
	EndpointIdentity     string
	EndpointPlatform     string
	Status               string
	TemplateVersionID    uuid.UUID
	Locale               string
	RecipientCiphertext  []byte
	RecipientKeyVersion  *int
	RecipientFingerprint []byte
	RenderedSubject      string
	RenderedTitle        string
	RenderedText         string
	RenderedHTML         string
	ProviderPayload      []byte
	ContentHash          []byte
	AttemptCount         int
	NextAttemptAt        *time.Time
	LeaseOwner           string
	LeaseExpiresAt       *time.Time
	ProviderMessageID    string
	LastErrorCode        string
	DeliveredAt          *time.Time
	SuppressedAt         *time.Time
	DeadAt               *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type DeliveryAttempt struct {
	ID                uuid.UUID
	DeliveryID        uuid.UUID
	AttemptNumber     int
	LeaseOwner        string
	Provider          string
	StartedAt         time.Time
	FinishedAt        *time.Time
	Result            string
	StatusClass       string
	ProviderMessageID string
	ErrorCode         string
	DurationMS        int
	ResponseExcerpt   string
	CreatedAt         time.Time
}

type UserSettings struct {
	UserID            uuid.UUID `json:"user_id"`
	Locale            string    `json:"locale"`
	Timezone          string    `json:"timezone"`
	QuietHoursEnabled bool      `json:"quiet_hours_enabled"`
	QuietHoursStart   *string   `json:"quiet_hours_start,omitempty"`
	QuietHoursEnd     *string   `json:"quiet_hours_end,omitempty"`
	DailyDigestHour   string    `json:"daily_digest_hour"`
	Version           int64     `json:"version"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Preference struct {
	ID       uuid.UUID `json:"id"`
	UserID   uuid.UUID `json:"user_id"`
	Category string    `json:"category"`
	Channel  string    `json:"channel"`
	Mode     string    `json:"mode"`
	Version  int64     `json:"version"`
}

type DeviceEndpoint struct {
	ID               uuid.UUID  `json:"id"`
	UserID           uuid.UUID  `json:"user_id"`
	Platform         string     `json:"platform"`
	DeviceName       string     `json:"device_name,omitempty"`
	TokenCiphertext  []byte     `json:"-"`
	TokenKeyVersion  int        `json:"-"`
	TokenFingerprint []byte     `json:"-"`
	TokenSuffix      string     `json:"token_suffix,omitempty"`
	Status           string     `json:"status"`
	LastSuccessAt    *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt    *time.Time `json:"last_failure_at,omitempty"`
	LastFailureCode  string     `json:"last_failure_code,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
}

type ChannelControl struct {
	Channel   string     `json:"channel"`
	State     string     `json:"state"`
	Reason    string     `json:"reason,omitempty"`
	ChangedBy string     `json:"changed_by"`
	ChangedAt time.Time  `json:"changed_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Version   int64      `json:"version"`
}

// PlannedEvent is the input to the atomic inbox/planner transaction.
type PlannedEvent struct {
	Inbox        EventInbox
	Notification Notification
	Deliveries   []Delivery
	DigestItems  []DigestRequest
}

type DigestRequest struct {
	NotificationID uuid.UUID
	UserID         uuid.UUID
	Locale         string
	Timezone       string
	LocalDate      time.Time
	WindowStart    time.Time
	WindowEnd      time.Time
	ScheduledAt    time.Time
}

type DigestItemContext struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	DeepLink string `json:"deep_link,omitempty"`
}

type DigestRenderContext struct {
	WindowDate string              `json:"window_date"`
	Items      []DigestItemContext `json:"items"`
	MoreCount  int                 `json:"more_count"`
}

type DigestWindow struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	Channel         string
	Locale          string
	Timezone        string
	LocalWindowDate time.Time
	WindowStartAt   time.Time
	WindowEndAt     time.Time
	ScheduledAt     time.Time
	Status          string
	ItemCount       int
	DeliveryID      *uuid.UUID
	LeaseOwner      string
	LeaseExpiresAt  *time.Time
}
