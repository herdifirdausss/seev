package model

import (
	"time"

	"github.com/google/uuid"
)

// CurrencyInfo is the public capability view of one registered currency.
// Operations are copied from the database policy row; callers must not infer
// capability from the presence of a currency alone.
type CurrencyInfo struct {
	Code         string
	MinorUnit    int16
	Status       string
	Operations   map[string]bool
	UserEnabled  bool
}

// CurrencyBalance is deliberately one row per currency. There is no
// cross-currency total because balances are not fungible without an explicit
// FX quote and conversion.
type CurrencyBalance struct {
	Currency  string
	MinorUnit int16
	Status    string
	Operations map[string]bool
	UserEnabled bool
	Available int64
	Hold      int64
	Pending   int64
	Frozen    int64
}

type FXPair struct {
	ID                  uuid.UUID
	PairCode            string
	BaseCurrency        string
	QuoteCurrency       string
	RateConvention      string
	Status              string
	RateSource          string
	PositionQualifier   string
	PairPolicyVersion   int64
	QuoteTTLSeconds     int
	RoundingMode        string
	Directions          []FXDirection
}

type FXDirection struct {
	ID                  uuid.UUID
	PairID              uuid.UUID
	SourceCurrency      string
	TargetCurrency      string
	Enabled             bool
	NewQuotesPaused     bool
	ConversionsPaused   bool
	MinSourceAmount     int64
	MaxSourceAmount     int64
	SpreadBasisPoints   int64
}

type FXQuote struct {
	ID                    uuid.UUID
	UserID                uuid.UUID
	PairID                uuid.UUID
	DirectionID           uuid.UUID
	RateVersionID         uuid.UUID
	SourceCurrency        string
	TargetCurrency        string
	SourceMinorUnit       int16
	TargetMinorUnit       int16
	SourceAmount          int64
	TargetAmount          int64
	ReferenceRate         string
	ClientRate            string
	RateConvention        string
	PairPolicyVersion     int64
	SpreadBasisPoints     int64
	RoundingMode          string
	RoundingRemainder     string
	RequestKey            string
	Status                string
	ExpiresAt             time.Time
	ConsumedAt            *time.Time
	ConsumedByConversion  uuid.UUID
	CreatedAt             time.Time
}

type FXConversion struct {
	ID                   uuid.UUID
	UserID               uuid.UUID
	QuoteID              uuid.UUID
	IdempotencyKey       string
	SourceCurrency       string
	TargetCurrency       string
	SourceAmount         int64
	TargetAmount         int64
	Status               string
	SourceTransactionID  uuid.UUID
	TargetTransactionID  uuid.UUID
	ErrorMessage         string
	CreatedAt            time.Time
	PostedAt             *time.Time
}

type FXRateVersion struct {
	ID            uuid.UUID
	PairID        uuid.UUID
	DirectionID   uuid.UUID
	Version       int64
	ReferenceRate string
	RateSource    string
	Status        string
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	CreatedBy     string
	SubmittedBy   string
	ApprovedBy    string
	CreatedAt     time.Time
	SubmittedAt   *time.Time
	ApprovedAt    *time.Time
	RetiredAt     *time.Time
}

type FXPosition struct {
	PairID                 uuid.UUID
	PairCode               string
	Currency               string
	AccountID              uuid.UUID
	MinorUnit              int16
	Balance                int64
	MinimumBalance         int64
	MaximumBalance         int64
	WarningMinimumBalance  int64
	WarningMaximumBalance  int64
	CriticalMinimumBalance int64
	CriticalMaximumBalance int64
	State                  string
	LastConversionAt       *time.Time
}

// FXDailyPosition is one currency-specific daily position proof. Conversion
// and rebalance amounts are kept as separate minor-unit fields so a report
// cannot imply that unlike currencies share one total.
type FXDailyPosition struct {
	Date             time.Time
	PairID           uuid.UUID
	PairCode         string
	Currency         string
	AccountID        uuid.UUID
	MinorUnit        int16
	OpeningBalance   int64
	ConversionInflow int64
	ConversionOutflow int64
	RebalanceCredit  int64
	RebalanceDebit   int64
	ClosingBalance   int64
	State            string
}

// FXConversionReconciliation is a read-only proof for one conversion or one
// orphan FX resource. A posted conversion is reconciled only when both
// single-currency ledger legs exist, are posted, point at the same
// conversion/quote/counterpart, carry the expected currency and amount, and
// balance independently.
type FXConversionReconciliation struct {
	// ResourceType/ResourceID identify the durable object that Assurance should
	// keep open when the evidence is not backed by a complete conversion row.
	// Normal rows use conversion/<ConversionID>; orphan quotes and FX legs use
	// quote/<QuoteID> and transaction/<TransactionID> respectively.
	ResourceType         string
	ResourceID           uuid.UUID
	ConversionID       uuid.UUID
	QuoteID            uuid.UUID
	SourceCurrency     string
	TargetCurrency     string
	SourceAmount       int64
	TargetAmount       int64
	SourceTransactionID uuid.UUID
	TargetTransactionID uuid.UUID
	SourceLegStatus    string
	TargetLegStatus    string
	SourceLinkValid    bool
	TargetLinkValid    bool
	SourceLegBalanced  bool
	TargetLegBalanced  bool
	QuoteValid         bool
	PositionAccountsValid bool
	PositionBalancesValid bool
	AggregateEventPresent bool
	Status             string
	Reason             string
	CheckedAt          time.Time
}

// FXReconciliationReport is a bounded report for operator dashboards and
// assurance jobs. Items never expose a cross-currency total.
type FXReconciliationReport struct {
	From      time.Time
	To        time.Time
	Total     int
	Reconciled int
	Critical  int
	Items     []FXConversionReconciliation
}
