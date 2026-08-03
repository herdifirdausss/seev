// Package events is the versioned wire-format contract for ledger outbox
// events (docs/roadmap/archive/14 Task T3, decision K4). It is the SINGLE subpackage of
// internal/ledger that external code (other modules, cmd/, internal/handler)
// may import — see docs/development/project-guide.md "Module Boundaries". It contains ONLY payload
// types and event-type constants: no repository, no processor, no DB access.
// A consumer that only needs to decode events must not be forced to pull in
// the whole ledger module's dependency graph.
//
// Delivery contract: at-least-once. Current consumers prefer the payload's
// deterministic EventID for logical deduplication and fall back to the
// RabbitMQ message_id (= outbox_events.id) for historical payloads. Ordering
// between events is NOT guaranteed.
//
// Versioning: a new OPTIONAL field on an existing type is NOT a breaking
// change (bump nothing). A changed or removed field, or a change in what a
// field means, requires a new SchemaVersion and — if consumers can't
// upgrade atomically — dual-publish both versions during the transition.
package events

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"time"

	"github.com/google/uuid"
)

var (
	minorAmountPattern = regexp.MustCompile(`^[0-9]+$`)
	currencyPattern    = regexp.MustCompile(`^[A-Z]{3}$`)
)

const (
	// TypeTransactionPosted covers every transaction type that reaches
	// status='posted' — money_in, transfer_p2p, withdraw_settle, reversal
	// itself (the reversal IS a posted transaction), etc. Consumers that
	// care about only some transaction types filter on
	// TransactionPosted.TransactionType; this keeps the event schema at two
	// types total instead of growing by one for every new transaction type
	// the registry gains (docs/roadmap/archive/08 S8's interest_accrue, for instance,
	// needs zero new event-schema work).
	TypeTransactionPosted = "ledger.transaction.posted.v1"
	// TypeTransactionReversed is emitted ADDITIONALLY (alongside a
	// TypeTransactionPosted for the reversal transaction itself) to notify
	// specifically that a prior transaction was reversed — routed against
	// the ORIGINAL transaction's AggregateID, not the reversal's, so a
	// consumer watching one transaction's lifecycle sees this without
	// having to correlate two different aggregate ids itself.
	TypeTransactionReversed = "ledger.transaction.reversed.v1"
	// TypeAdjustmentDecided is emitted when a pending_adjustments row is
	// approved or rejected (docs/roadmap/archive/16 Task T1, decision K8) — the
	// governance audit trail (who requested, who decided, what) rides the
	// same outbox mechanism as every other ledger event.
	TypeAdjustmentDecided = "ledger.adjustment.decided.v1"
	// Interest events are emitted by the C5 period-close workflow through the
	// Ledger outbox.  Their logical EventID is stable for the durable evidence
	// row, so replaying a worker cannot create a second logical domain event.
	TypeInterestAccrued       = "ledger.interest_accrued.v1"
	TypeInterestCapitalized   = "ledger.interest_capitalized.v1"
	TypeInterestPeriodClosed  = "ledger.interest_period.closed.v1"
	TypeInterestAdjusted      = "ledger.interest_adjusted.v1"
	TypeScheduleOccurrenceSucceeded = "ledger.schedule.occurrence.succeeded.v1"
	TypeScheduleOccurrenceFailed    = "ledger.schedule.occurrence.failed.v1"
	TypeSchedulePaused               = "ledger.schedule.paused.v1"
)

type InterestAccrued struct {
	SchemaVersion       int        `json:"schema_version"`
	EventID             *uuid.UUID `json:"event_id,omitempty"`
	AccrualID           uuid.UUID  `json:"accrual_id"`
	PeriodID            uuid.UUID  `json:"period_id"`
	EnrollmentID        uuid.UUID  `json:"enrollment_id"`
	AccountID           uuid.UUID  `json:"account_id"`
	AccrualDate         string     `json:"accrual_date"`
	Amount              string     `json:"amount"`
	Currency            string     `json:"currency"`
	LedgerTransactionID *uuid.UUID `json:"ledger_transaction_id,omitempty"`
	OccurredAt          time.Time  `json:"occurred_at"`
}

func NewInterestAccrued(accrualID, periodID, enrollmentID, accountID uuid.UUID, accrualDate, amount, currency string, transactionID *uuid.UUID, occurredAt time.Time) InterestAccrued {
	return InterestAccrued{
		SchemaVersion: 1,
		EventID: logicalEventID(TypeInterestAccrued, accrualID.String()),
		AccrualID: accrualID, PeriodID: periodID, EnrollmentID: enrollmentID,
		AccountID: accountID, AccrualDate: accrualDate, Amount: amount,
		Currency: currency, LedgerTransactionID: transactionID, OccurredAt: occurredAt,
	}
}

func (e InterestAccrued) ToPayload() map[string]any { return toPayload(e) }

type InterestCapitalized struct {
	SchemaVersion       int        `json:"schema_version"`
	EventID             *uuid.UUID `json:"event_id,omitempty"`
	CapitalizationID    uuid.UUID  `json:"capitalization_id"`
	PeriodID            uuid.UUID  `json:"period_id"`
	EnrollmentID        uuid.UUID  `json:"enrollment_id"`
	AccountID           uuid.UUID  `json:"account_id"`
	Amount              string     `json:"amount"`
	Currency            string     `json:"currency"`
	LedgerTransactionID *uuid.UUID `json:"ledger_transaction_id,omitempty"`
	OccurredAt          time.Time  `json:"occurred_at"`
}

func NewInterestCapitalized(capitalizationID, periodID, enrollmentID, accountID uuid.UUID, amount, currency string, transactionID *uuid.UUID, occurredAt time.Time) InterestCapitalized {
	return InterestCapitalized{
		SchemaVersion: 1,
		EventID: logicalEventID(TypeInterestCapitalized, capitalizationID.String()),
		CapitalizationID: capitalizationID, PeriodID: periodID, EnrollmentID: enrollmentID,
		AccountID: accountID, Amount: amount, Currency: currency,
		LedgerTransactionID: transactionID, OccurredAt: occurredAt,
	}
}

func (e InterestCapitalized) ToPayload() map[string]any { return toPayload(e) }

type InterestPeriodClosed struct {
	SchemaVersion          int        `json:"schema_version"`
	EventID                *uuid.UUID `json:"event_id,omitempty"`
	PeriodID               uuid.UUID  `json:"period_id"`
	ProductID              uuid.UUID  `json:"product_id"`
	Currency               string     `json:"currency"`
	PeriodYear             int        `json:"period_year"`
	PeriodMonth            int        `json:"period_month"`
	TotalAccruedAmount     string     `json:"total_accrued_amount"`
	TotalCapitalizedAmount string     `json:"total_capitalized_amount"`
	ClosedAt               time.Time  `json:"closed_at"`
}

func NewInterestPeriodClosed(periodID, productID uuid.UUID, currency string, year, month int, accrued, capitalized string, closedAt time.Time) InterestPeriodClosed {
	return InterestPeriodClosed{
		SchemaVersion: 1,
		EventID: logicalEventID(TypeInterestPeriodClosed, periodID.String()),
		PeriodID: periodID, ProductID: productID, Currency: currency,
		PeriodYear: year, PeriodMonth: month, TotalAccruedAmount: accrued,
		TotalCapitalizedAmount: capitalized, ClosedAt: closedAt,
	}
}

func (e InterestPeriodClosed) ToPayload() map[string]any { return toPayload(e) }

type InterestAdjusted struct {
	SchemaVersion       int        `json:"schema_version"`
	EventID             *uuid.UUID `json:"event_id,omitempty"`
	AdjustmentID        uuid.UUID  `json:"adjustment_id"`
	SourcePeriodID      uuid.UUID  `json:"source_period_id"`
	EnrollmentID        uuid.UUID  `json:"enrollment_id"`
	Amount              string     `json:"amount"`
	Direction           string     `json:"direction"`
	CorrectionStage     string     `json:"correction_stage"`
	Currency            string     `json:"currency"`
	LedgerTransactionID *uuid.UUID `json:"ledger_transaction_id,omitempty"`
	OccurredAt          time.Time  `json:"occurred_at"`
}

func NewInterestAdjusted(adjustmentID, sourcePeriodID, enrollmentID uuid.UUID, amount, direction, stage, currency string, transactionID *uuid.UUID, occurredAt time.Time) InterestAdjusted {
	return InterestAdjusted{
		SchemaVersion: 1,
		EventID: logicalEventID(TypeInterestAdjusted, adjustmentID.String()),
		AdjustmentID: adjustmentID, SourcePeriodID: sourcePeriodID, EnrollmentID: enrollmentID,
		Amount: amount, Direction: direction, CorrectionStage: stage, Currency: currency,
		LedgerTransactionID: transactionID, OccurredAt: occurredAt,
	}
}

func (e InterestAdjusted) ToPayload() map[string]any { return toPayload(e) }

type ScheduleOccurrenceSucceeded struct {
	SchemaVersion       int        `json:"schema_version"`
	EventID             *uuid.UUID `json:"event_id,omitempty"`
	OccurrenceID        uuid.UUID  `json:"occurrence_id"`
	ScheduleID          uuid.UUID  `json:"schedule_id"`
	LedgerTransactionID *uuid.UUID `json:"ledger_transaction_id,omitempty"`
	Amount              string     `json:"amount"`
	FeeAmount           string     `json:"fee_amount"`
	OccurredAt          time.Time  `json:"occurred_at"`
}

func NewScheduleOccurrenceSucceeded(occurrenceID, scheduleID uuid.UUID, transactionID *uuid.UUID, amount, feeAmount string, occurredAt time.Time) ScheduleOccurrenceSucceeded {
	return ScheduleOccurrenceSucceeded{
		SchemaVersion: 1,
		EventID: logicalEventID(TypeScheduleOccurrenceSucceeded, occurrenceID.String()),
		OccurrenceID: occurrenceID, ScheduleID: scheduleID, LedgerTransactionID: transactionID,
		Amount: amount, FeeAmount: feeAmount, OccurredAt: occurredAt,
	}
}

func (e ScheduleOccurrenceSucceeded) ToPayload() map[string]any { return toPayload(e) }

type ScheduleOccurrenceFailed struct {
	SchemaVersion int        `json:"schema_version"`
	EventID       *uuid.UUID `json:"event_id,omitempty"`
	OccurrenceID  uuid.UUID  `json:"occurrence_id"`
	ScheduleID    uuid.UUID  `json:"schedule_id"`
	Status        string     `json:"status"`
	ErrorCode     string     `json:"error_code"`
	Retryable     bool       `json:"retryable"`
	OccurredAt    time.Time  `json:"occurred_at"`
}

func NewScheduleOccurrenceFailed(occurrenceID, scheduleID uuid.UUID, status, errorCode string, retryable bool, occurredAt time.Time) ScheduleOccurrenceFailed {
	return ScheduleOccurrenceFailed{
		SchemaVersion: 1,
		EventID: logicalEventID(TypeScheduleOccurrenceFailed, occurrenceID.String()+":"+status),
		OccurrenceID: occurrenceID, ScheduleID: scheduleID, Status: status,
		ErrorCode: errorCode, Retryable: retryable, OccurredAt: occurredAt,
	}
}

func (e ScheduleOccurrenceFailed) ToPayload() map[string]any { return toPayload(e) }

type SchedulePaused struct {
	SchemaVersion int        `json:"schema_version"`
	EventID       *uuid.UUID `json:"event_id,omitempty"`
	ScheduleID    uuid.UUID  `json:"schedule_id"`
	Reason        string     `json:"reason"`
	OccurredAt    time.Time  `json:"occurred_at"`
}

func NewSchedulePaused(scheduleID uuid.UUID, reason string, occurredAt time.Time) SchedulePaused {
	return SchedulePaused{
		SchemaVersion: 1,
		EventID: logicalEventID(TypeSchedulePaused, scheduleID.String()+":"+reason),
		ScheduleID: scheduleID, Reason: reason, OccurredAt: occurredAt,
	}
}

func (e SchedulePaused) ToPayload() map[string]any { return toPayload(e) }

// EntrySummary is one posted ledger_entries row, reduced to the fields a
// consumer needs to reconstruct the double-entry movement without querying
// the ledger directly. Amount is always a string (minor units) — never a
// JSON number, to avoid float precision loss in consumers.
type EntrySummary struct {
	AccountID uuid.UUID `json:"account_id"`
	Direction string    `json:"direction"`
	Amount    string    `json:"amount"`
}

// TransactionPosted is the payload for TypeTransactionPosted.
type TransactionPosted struct {
	SchemaVersion int `json:"schema_version"`
	// EventID is stable across versioned representations of one logical event.
	EventID *uuid.UUID `json:"event_id,omitempty"`
	TxID    uuid.UUID  `json:"tx_id"`
	// TransactionType is the registry key (money_in, transfer_p2p, ...) —
	// consumers filter on this instead of subscribing to per-type routing
	// keys.
	TransactionType string `json:"transaction_type"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency"`
	// SourceAccountID/DestinationAccountID are nil when the transaction
	// isn't a single source->destination pair (docs/roadmap/archive/14 Task T1,
	// decision K2 — e.g. Reversal).
	SourceAccountID      *uuid.UUID     `json:"source_account_id,omitempty"`
	DestinationAccountID *uuid.UUID     `json:"destination_account_id,omitempty"`
	Entries              []EntrySummary `json:"entries"`
	ExternalRef          string         `json:"external_ref,omitempty"`
	OccurredAt           time.Time      `json:"occurred_at"`
	// UserID/TargetUserID (docs/roadmap/archive/25 Task T4) are the Command's own
	// UserID/TargetUserID, added SPECIFICALLY so a consumer (internal/notify)
	// can determine WHICH user(s) to notify without querying the ledger back
	// — a new OPTIONAL field, non-breaking per this package's own versioning
	// policy (no SchemaVersion bump). Both nil for transaction types with no
	// end-user party (e.g. an internal system-only posting); TargetUserID
	// nil for anything that isn't a two-user transfer (transfer_p2p).
	UserID       *uuid.UUID `json:"user_id,omitempty"`
	TargetUserID *uuid.UUID `json:"target_user_id,omitempty"`
	// MerchantTenantID (Plan 57 T5) is the Command's own MerchantTenantID,
	// added SPECIFICALLY so a consumer (T7's webhook relay) can route this
	// event to the right merchant tenant without querying the ledger back
	// — same "new OPTIONAL field, non-breaking" precedent as UserID/
	// TargetUserID above. nil for every transaction type except
	// merchant_transfer.
	MerchantTenantID *uuid.UUID `json:"merchant_tenant_id,omitempty"`
	// RequestID (docs/roadmap/archive/36 Task T4) is the originating HTTP/gRPC
	// request_id, added SPECIFICALLY so the outbox relay — a background
	// worker with no request ctx of its own — can restore it as the AMQP
	// CorrelationId when publishing. A new OPTIONAL field, non-breaking per
	// this package's own versioning policy (no SchemaVersion bump). Empty
	// for events built outside a traced request (e.g. some system jobs).
	RequestID string `json:"request_id,omitempty"`
	// C5 top-up fields are additive: Amount remains the wallet principal while
	// TotalDebit is the provider-collected amount when a fee is charged on top.
	FeeAmount      string     `json:"fee_amount,omitempty"`
	TotalDebit     string     `json:"total_debit,omitempty"`
	FeeGateway     string     `json:"fee_gateway,omitempty"`
	FeeApplication string     `json:"fee_application,omitempty"`
	FeeQuoteID     *uuid.UUID `json:"fee_quote_id,omitempty"`
	PayinID        *uuid.UUID `json:"payin_id,omitempty"`
}

// Validate checks the semantic invariants that JSON decoding alone cannot
// enforce. Consumers call this before applying any side effect so malformed
// known-version messages are rejected deterministically while unknown
// optional fields remain tolerated by encoding/json.
func (e TransactionPosted) Validate() error {
	if e.SchemaVersion != 1 {
		return fmt.Errorf("transaction posted: unsupported schema_version %d", e.SchemaVersion)
	}
	if e.TxID == uuid.Nil || e.TransactionType == "" || !minorAmountPattern.MatchString(e.Amount) || !currencyPattern.MatchString(e.Currency) || e.OccurredAt.IsZero() {
		return fmt.Errorf("transaction posted: required fields are invalid")
	}
	if (e.FeeAmount != "" && !minorAmountPattern.MatchString(e.FeeAmount)) ||
		(e.TotalDebit != "" && !minorAmountPattern.MatchString(e.TotalDebit)) {
		return fmt.Errorf("transaction posted: fee amounts are invalid")
	}
	if (e.FeeAmount == "") != (e.TotalDebit == "") {
		return fmt.Errorf("transaction posted: fee_amount and total_debit must be supplied together")
	}
	if e.FeeAmount != "" {
		amount, amountOK := new(big.Int).SetString(e.Amount, 10)
		fee, feeOK := new(big.Int).SetString(e.FeeAmount, 10)
		total, totalOK := new(big.Int).SetString(e.TotalDebit, 10)
		if !amountOK || !feeOK || !totalOK {
			return fmt.Errorf("transaction posted: fee amounts are invalid")
		}
		expected := new(big.Int).Add(amount, fee)
		if expected.Cmp(total) != 0 {
			return fmt.Errorf("transaction posted: total_debit must equal amount plus fee_amount")
		}
	}
	for i, entry := range e.Entries {
		if entry.AccountID == uuid.Nil || (entry.Direction != "debit" && entry.Direction != "credit") || !minorAmountPattern.MatchString(entry.Amount) {
			return fmt.Errorf("transaction posted: invalid entry %d", i)
		}
	}
	return nil
}

// NewTransactionPosted builds a TransactionPosted at the current schema
// version. Takes plain values rather than any internal/ledger type — this
// package cannot import processors or model without creating an import
// cycle (processors imports events, not the other way around).
func NewTransactionPosted(
	txID uuid.UUID,
	transactionType, amount, currency string,
	source, destination *uuid.UUID,
	entries []EntrySummary,
	externalRef string,
	occurredAt time.Time,
	userID, targetUserID *uuid.UUID,
	requestID string,
	merchantTenantID *uuid.UUID,
) TransactionPosted {
	return TransactionPosted{
		SchemaVersion:        1,
		EventID:              logicalEventID(TypeTransactionPosted, txID.String()),
		TxID:                 txID,
		TransactionType:      transactionType,
		Amount:               amount,
		Currency:             currency,
		SourceAccountID:      source,
		DestinationAccountID: destination,
		Entries:              entries,
		ExternalRef:          externalRef,
		OccurredAt:           occurredAt,
		UserID:               userID,
		TargetUserID:         targetUserID,
		RequestID:            requestID,
		MerchantTenantID:     merchantTenantID,
	}
}

// ToPayload converts a TransactionPosted to the map[string]any shape
// outbox_events.Payload stores, via a JSON round-trip — this guarantees the
// stored payload is byte-for-byte what json.Marshal(TransactionPosted{...})
// would produce (string amount, RFC3339 timestamp, omitted empty fields),
// with no risk of the map construction drifting from the struct's own json
// tags over time.
func (e TransactionPosted) ToPayload() map[string]any { return toPayload(e) }

// TransactionReversed is the payload for TypeTransactionReversed, routed
// against the ORIGINAL transaction's AggregateID.
type TransactionReversed struct {
	SchemaVersion int        `json:"schema_version"`
	EventID       *uuid.UUID `json:"event_id,omitempty"`
	ReversalTxID  uuid.UUID  `json:"reversal_tx_id"`
	OriginalTxID  uuid.UUID  `json:"original_tx_id"`
	Amount        string     `json:"amount"`
	Currency      string     `json:"currency"`
	OccurredAt    time.Time  `json:"occurred_at"`
}

// Validate applies the v1 wire invariants before a consumer uses a reversal.
func (e TransactionReversed) Validate() error {
	if e.SchemaVersion != 1 || e.ReversalTxID == uuid.Nil || e.OriginalTxID == uuid.Nil || !minorAmountPattern.MatchString(e.Amount) || !currencyPattern.MatchString(e.Currency) || e.OccurredAt.IsZero() {
		return fmt.Errorf("transaction reversed: required fields are invalid")
	}
	return nil
}

// NewTransactionReversed builds a TransactionReversed at the current schema
// version.
func NewTransactionReversed(reversalTxID, originalTxID uuid.UUID, amount, currency string, occurredAt time.Time) TransactionReversed {
	return TransactionReversed{
		SchemaVersion: 1,
		EventID:       logicalEventID(TypeTransactionReversed, reversalTxID.String()+":"+originalTxID.String()),
		ReversalTxID:  reversalTxID,
		OriginalTxID:  originalTxID,
		Amount:        amount,
		Currency:      currency,
		OccurredAt:    occurredAt,
	}
}

// ToPayload converts a TransactionReversed to the map[string]any shape
// outbox_events.Payload stores — see TransactionPosted.ToPayload.
func (e TransactionReversed) ToPayload() map[string]any { return toPayload(e) }

// AdjustmentDecided is the payload for TypeAdjustmentDecided, routed against
// the pending_adjustments row's own id as AggregateID.
type AdjustmentDecided struct {
	SchemaVersion int        `json:"schema_version"`
	EventID       *uuid.UUID `json:"event_id,omitempty"`
	PendingID     uuid.UUID  `json:"pending_id"`
	RequestedBy   string     `json:"requested_by"`
	ApprovedBy    string     `json:"approved_by"`
	// Decision is "approved" or "rejected".
	Decision string `json:"decision"`
	// ExecutedTxID is nil for a rejection (no money moved).
	ExecutedTxID *uuid.UUID `json:"executed_tx_id,omitempty"`
	OccurredAt   time.Time  `json:"occurred_at"`
}

// Validate applies the v1 wire invariants before a consumer uses a decision.
func (e AdjustmentDecided) Validate() error {
	if e.SchemaVersion != 1 || e.PendingID == uuid.Nil || e.RequestedBy == "" || e.ApprovedBy == "" || (e.Decision != "approved" && e.Decision != "rejected") || e.OccurredAt.IsZero() {
		return fmt.Errorf("adjustment decided: required fields are invalid")
	}
	return nil
}

// NewAdjustmentDecided builds an AdjustmentDecided at the current schema
// version.
func NewAdjustmentDecided(pendingID uuid.UUID, requestedBy, approvedBy, decision string, executedTxID *uuid.UUID, occurredAt time.Time) AdjustmentDecided {
	return AdjustmentDecided{
		SchemaVersion: 1,
		EventID:       logicalEventID(TypeAdjustmentDecided, pendingID.String()+":"+decision),
		PendingID:     pendingID,
		RequestedBy:   requestedBy,
		ApprovedBy:    approvedBy,
		Decision:      decision,
		ExecutedTxID:  executedTxID,
		OccurredAt:    occurredAt,
	}
}

// logicalEventID is deterministic for one logical domain event. A future
// v1/v2 dual-publish therefore carries the same ID in both outbox rows while
// each AMQP message keeps its own delivery ID.
func logicalEventID(eventType, identity string) *uuid.UUID {
	id := uuid.NewSHA1(uuid.Nil, []byte(eventType+":"+identity))
	return &id
}

// ToPayload converts an AdjustmentDecided to the map[string]any shape
// outbox_events.Payload stores — see TransactionPosted.ToPayload.
func (e AdjustmentDecided) ToPayload() map[string]any { return toPayload(e) }

func toPayload(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		// v is always one of this package's own struct types — a marshal
		// failure here means a programming error (e.g. an unsupported field
		// type added to the struct), not a runtime/data condition a caller
		// could meaningfully recover from.
		panic("events: marshal payload: " + err.Error())
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		panic("events: unmarshal payload: " + err.Error())
	}
	return m
}
