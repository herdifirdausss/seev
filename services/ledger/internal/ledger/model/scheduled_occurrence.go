package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	ScheduleMissedSkip           = "skip"
	ScheduleMissedRunOnceLatest  = "run_once_latest"
	ScheduleMissedCatchUpBounded = "catch_up_bounded"

	ScheduleOccurrencePlanned           = "planned"
	ScheduleOccurrenceDue               = "due"
	ScheduleOccurrenceScreening         = "screening"
	ScheduleOccurrenceReady             = "ready"
	ScheduleOccurrenceProcessing        = "processing"
	ScheduleOccurrenceRetryWait         = "retry_wait"
	ScheduleOccurrenceSucceeded         = "succeeded"
	ScheduleOccurrenceFailedBusiness    = "failed_business"
	ScheduleOccurrenceFailedTerminal    = "failed_terminal"
	ScheduleOccurrenceBlocked           = "blocked"
	ScheduleOccurrenceSkippedMissed     = "skipped_missed"
	ScheduleOccurrenceSkippedSuperseded = "skipped_superseded"
	ScheduleOccurrenceCancelled         = "cancelled"
	ScheduleOccurrenceExpired           = "expired"
)

type ScheduledOccurrence struct {
	ID                  uuid.UUID
	PublicID            string
	ScheduleID          uuid.UUID
	ScheduleVersion     int64
	ScheduledFor        time.Time
	ScheduledLocalDate  time.Time
	Status              string
	IdempotencyKey      string
	PolicySnapshot      json.RawMessage
	FeeAmount           *int64
	FeeQuoteID          *uuid.UUID
	LedgerTransactionID *uuid.UUID
	AttemptCount        int
	NextAttemptAt       *time.Time
	LeaseOwner          *string
	LeaseExpiresAt      *time.Time
	ErrorCode           *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type ScheduledExecutionAttempt struct {
	ID                  uuid.UUID
	OccurrenceID        uuid.UUID
	AttemptNumber       int
	Phase               string
	Result              string
	Retryable           bool
	ErrorCode           *string
	LedgerTransactionID *uuid.UUID
	StartedAt           time.Time
	FinishedAt          *time.Time
	CreatedAt           time.Time
}

type ScheduledPolicy struct {
	MissedRunPolicy             string `json:"missed_run_policy"`
	CatchUpLimit                int    `json:"catch_up_limit"`
	MaxInfrastructureAttempts   int    `json:"max_infrastructure_attempts"`
	RetryWindowSeconds          int64  `json:"retry_window_seconds"`
	ConsecutiveFailureThreshold int    `json:"consecutive_failure_threshold"`
	FeeMode                     string `json:"fee_mode"`
	MaxFeeAmount                *int64 `json:"max_fee_amount,omitempty"`
}

type ScheduleCommand struct {
	Type         string         `json:"type"`
	Version      int            `json:"version"`
	Amount       string         `json:"amount"`
	Currency     string         `json:"currency,omitempty"`
	TargetUserID uuid.UUID      `json:"target_user_id,omitempty"`
	PocketCode   string         `json:"pocket_code,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}
