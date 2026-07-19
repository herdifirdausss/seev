// Package events is the versioned wire-format contract for ledger outbox
// events (docs/plan/14 Task T3, decision K4). It is the SINGLE subpackage of
// internal/ledger that external code (other modules, cmd/, internal/handler)
// may import — see PROJECT_GUIDE.md "Module Boundaries". It contains ONLY payload
// types and event-type constants: no repository, no processor, no DB access.
// A consumer that only needs to decode events must not be forced to pull in
// the whole ledger module's dependency graph.
//
// Delivery contract: at-least-once. RabbitMQ message_id = outbox_events.id —
// consumers MUST dedup by that id. Ordering between events is NOT
// guaranteed.
//
// Versioning: a new OPTIONAL field on an existing type is NOT a breaking
// change (bump nothing). A changed or removed field, or a change in what a
// field means, requires a new SchemaVersion and — if consumers can't
// upgrade atomically — dual-publish both versions during the transition.
package events

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	// TypeTransactionPosted covers every transaction type that reaches
	// status='posted' — money_in, transfer_p2p, withdraw_settle, reversal
	// itself (the reversal IS a posted transaction), etc. Consumers that
	// care about only some transaction types filter on
	// TransactionPosted.TransactionType; this keeps the event schema at two
	// types total instead of growing by one for every new transaction type
	// the registry gains (docs/plan/08 S8's interest_accrue, for instance,
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
	// approved or rejected (docs/plan/16 Task T1, decision K8) — the
	// governance audit trail (who requested, who decided, what) rides the
	// same outbox mechanism as every other ledger event.
	TypeAdjustmentDecided = "ledger.adjustment.decided.v1"
)

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
	SchemaVersion int       `json:"schema_version"`
	TxID          uuid.UUID `json:"tx_id"`
	// TransactionType is the registry key (money_in, transfer_p2p, ...) —
	// consumers filter on this instead of subscribing to per-type routing
	// keys.
	TransactionType string `json:"transaction_type"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency"`
	// SourceAccountID/DestinationAccountID are nil when the transaction
	// isn't a single source->destination pair (docs/plan/14 Task T1,
	// decision K2 — e.g. Reversal).
	SourceAccountID      *uuid.UUID     `json:"source_account_id,omitempty"`
	DestinationAccountID *uuid.UUID     `json:"destination_account_id,omitempty"`
	Entries              []EntrySummary `json:"entries"`
	ExternalRef          string         `json:"external_ref,omitempty"`
	OccurredAt           time.Time      `json:"occurred_at"`
	// UserID/TargetUserID (docs/plan/25 Task T4) are the Command's own
	// UserID/TargetUserID, added SPECIFICALLY so a consumer (internal/notify)
	// can determine WHICH user(s) to notify without querying the ledger back
	// — a new OPTIONAL field, non-breaking per this package's own versioning
	// policy (no SchemaVersion bump). Both nil for transaction types with no
	// end-user party (e.g. an internal system-only posting); TargetUserID
	// nil for anything that isn't a two-user transfer (transfer_p2p).
	UserID       *uuid.UUID `json:"user_id,omitempty"`
	TargetUserID *uuid.UUID `json:"target_user_id,omitempty"`
	// RequestID (docs/plan/36 Task T4) is the originating HTTP/gRPC
	// request_id, added SPECIFICALLY so the outbox relay — a background
	// worker with no request ctx of its own — can restore it as the AMQP
	// CorrelationId when publishing. A new OPTIONAL field, non-breaking per
	// this package's own versioning policy (no SchemaVersion bump). Empty
	// for events built outside a traced request (e.g. some system jobs).
	RequestID string `json:"request_id,omitempty"`
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
) TransactionPosted {
	return TransactionPosted{
		SchemaVersion:        1,
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
	SchemaVersion int       `json:"schema_version"`
	ReversalTxID  uuid.UUID `json:"reversal_tx_id"`
	OriginalTxID  uuid.UUID `json:"original_tx_id"`
	Amount        string    `json:"amount"`
	Currency      string    `json:"currency"`
	OccurredAt    time.Time `json:"occurred_at"`
}

// NewTransactionReversed builds a TransactionReversed at the current schema
// version.
func NewTransactionReversed(reversalTxID, originalTxID uuid.UUID, amount, currency string, occurredAt time.Time) TransactionReversed {
	return TransactionReversed{
		SchemaVersion: 1,
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
	SchemaVersion int       `json:"schema_version"`
	PendingID     uuid.UUID `json:"pending_id"`
	RequestedBy   string    `json:"requested_by"`
	ApprovedBy    string    `json:"approved_by"`
	// Decision is "approved" or "rejected".
	Decision string `json:"decision"`
	// ExecutedTxID is nil for a rejection (no money moved).
	ExecutedTxID *uuid.UUID `json:"executed_tx_id,omitempty"`
	OccurredAt   time.Time  `json:"occurred_at"`
}

// NewAdjustmentDecided builds an AdjustmentDecided at the current schema
// version.
func NewAdjustmentDecided(pendingID uuid.UUID, requestedBy, approvedBy, decision string, executedTxID *uuid.UUID, occurredAt time.Time) AdjustmentDecided {
	return AdjustmentDecided{
		SchemaVersion: 1,
		PendingID:     pendingID,
		RequestedBy:   requestedBy,
		ApprovedBy:    approvedBy,
		Decision:      decision,
		ExecutedTxID:  executedTxID,
		OccurredAt:    occurredAt,
	}
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
