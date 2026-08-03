package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// SavingsProduct is the versioned product definition used by the monthly
// interest path.  It is deliberately separate from the legacy savings_config
// projection so historical daily-capitalisation rows remain readable.
type SavingsProduct struct {
	ID                       uuid.UUID
	PublicID                 string
	ProductCode              string
	Name                     string
	Currency                 string
	EligibleAccountTypes     []string
	Status                   string
	DayCountConvention       string
	CapitalizationFrequency  string
	Timezone                 string
	MinimumEligibleBalance   int64
	InterestExpenseAccountID uuid.UUID
	InterestPayableAccountID uuid.UUID
	DefaultRatePolicy        string
	Version                  int64
	CreatedBy                string
	UpdatedBy                string
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type SavingsRateVersion struct {
	ID              uuid.UUID
	PublicID        string
	ProductID       uuid.UUID
	AnnualRateBps   int
	Status          string
	EffectiveFrom   time.Time
	EffectiveUntil  *time.Time
	ContentHash     []byte
	CreatedBy       string
	SubmittedBy     *string
	ApprovedBy      *string
	RejectedBy      *string
	CreatedAt       time.Time
	SubmittedAt     *time.Time
	ApprovedAt      *time.Time
	RetiredAt       *time.Time
	RejectionReason *string
}

type SavingsEnrollment struct {
	ID                 uuid.UUID
	PublicID           string
	ProductID          uuid.UUID
	AccountID          uuid.UUID
	UserID             uuid.UUID
	Status             string
	Mode               string
	EffectiveFrom      time.Time
	EffectiveUntil     *time.Time
	CarryNumerator     string
	CarryDenominator   string
	Version            int64
	CreatedBy          string
	UpdatedBy          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type InterestPeriod struct {
	ID                    uuid.UUID
	PublicID              string
	ProductID             uuid.UUID
	Currency              string
	PeriodYear            int
	PeriodMonth           int
	PeriodStartAt         time.Time
	PeriodEndAt           time.Time
	AccrualCutoffAt       time.Time
	CloseNotBeforeAt      time.Time
	Status                string
	ExpectedItemCount     int64
	CompletedItemCount    int64
	BlockedItemCount      int64
	TotalAccruedAmount    int64
	TotalCapitalizedAmount int64
	OpenedAt              *time.Time
	ClosingStartedAt      *time.Time
	ClosedAt              *time.Time
	FailedAt              *time.Time
	LastErrorCode         *string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type InterestDailyAccrual struct {
	ID                      uuid.UUID
	PeriodID                uuid.UUID
	EnrollmentID            uuid.UUID
	AccountID               uuid.UUID
	AccrualDate             time.Time
	SnapshotID              *uuid.UUID
	ClosingBalance          *int64
	RateVersionID           *uuid.UUID
	AnnualRateBps           *int
	ExactNumerator          string
	Denominator             string
	OpeningCarryNumerator   string
	RecognizedAmount        *int64
	ClosingCarryNumerator   string
	Status                  string
	AttemptCount            int
	NextAttemptAt           *time.Time
	LeaseOwner              *string
	LeaseExpiresAt          *time.Time
	LedgerTransactionID     *uuid.UUID
	ErrorCode               *string
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type InterestCapitalizationItem struct {
	ID                    uuid.UUID
	PeriodID              uuid.UUID
	EnrollmentID          uuid.UUID
	AccountID             uuid.UUID
	CapitalizationAmount  int64
	Status                string
	AttemptCount          int
	NextAttemptAt         *time.Time
	LeaseOwner            *string
	LeaseExpiresAt        *time.Time
	LedgerTransactionID   *uuid.UUID
	ErrorCode             *string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type InterestPeriodCheck struct {
	ID             uuid.UUID
	PeriodID       uuid.UUID
	CheckName      string
	Status         string
	ExpectedValue  *string
	ActualValue    *string
	Severity       string
	Details        json.RawMessage
	CheckedAt      time.Time
}

type InterestAdjustment struct {
	ID                       uuid.UUID
	PublicID                 string
	SourcePeriodID           uuid.UUID
	EnrollmentID             uuid.UUID
	SourceAccrualID         *uuid.UUID
	SourceCapitalizationID  *uuid.UUID
	Amount                   int64
	Direction                string
	Status                   string
	Reason                   string
	CreatedBy                string
	ApprovedBy               *string
	LedgerTransactionID      *uuid.UUID
	CreatedAt                time.Time
	ApprovedAt               *time.Time
	PostedAt                 *time.Time
}

const (
	SavingsProductDraft       = "draft"
	SavingsProductActive      = "active"
	SavingsProductIntakePaused = "intake_paused"
	SavingsProductRetired     = "retired"

	SavingsEnrollmentPending      = "pending"
	SavingsEnrollmentActive       = "active"
	SavingsEnrollmentAccrualPaused = "accrual_paused"
	SavingsEnrollmentEnded        = "ended"

	InterestPeriodPlanned  = "planned"
	InterestPeriodOpen     = "open"
	InterestPeriodClosing  = "closing"
	InterestPeriodClosed   = "closed"
	InterestPeriodFailed   = "failed"
	InterestPeriodCancelled = "cancelled_before_open"

	InterestAccrualPending         = "pending"
	InterestAccrualProcessing      = "processing"
	InterestAccrualCompletedZero   = "completed_zero"
	InterestAccrualCompletedPosted = "completed_posted"
	InterestAccrualRetryWait       = "retry_wait"
	InterestAccrualBlocked         = "blocked"
	InterestAccrualFailed          = "failed"
	InterestAccrualAdjusted        = "adjusted"

	InterestCapitalizationPending       = "pending"
	InterestCapitalizationProcessing    = "processing"
	InterestCapitalizationPosted        = "posted"
	InterestCapitalizationCompletedZero = "completed_zero"
	InterestCapitalizationRetryWait     = "retry_wait"
	InterestCapitalizationBlocked       = "blocked"
	InterestCapitalizationFailed        = "failed"
	InterestCapitalizationAdjusted      = "adjusted"
)
