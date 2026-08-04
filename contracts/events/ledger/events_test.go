package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Golden JSON tests lock the wire format (docs/roadmap/archive/14 Task T3) — a change
// to a json tag, field type, or omitempty behavior fails these tests, which
// is the point: the contract says at-least-once delivery to consumers who
// may not upgrade in lockstep with this repo, so the wire shape must not
// drift silently.

func fixedTime() time.Time {
	return time.Date(2026, 7, 11, 10, 30, 0, 0, time.UTC)
}

func TestTransactionPosted_GoldenJSON_FullFields(t *testing.T) {
	txID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	src := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	dst := uuid.MustParse("00000000-0000-0000-0000-000000000003")

	ev := NewTransactionPosted(
		txID, "money_in", "100000", "IDR", &src, &dst,
		[]EntrySummary{
			{AccountID: src, Direction: "debit", Amount: "100000"},
			{AccountID: dst, Direction: "credit", Amount: "100000"},
		},
		"ext-ref-123",
		fixedTime(),
		nil, nil, "", nil,
	)

	b, err := json.Marshal(ev)
	require.NoError(t, err)

	want := `{
		"schema_version": 1,
		"event_id": "f2799b13-5f6f-5e0b-aceb-333a5c6cfcd7",
		"tx_id": "00000000-0000-0000-0000-000000000001",
		"transaction_type": "money_in",
		"amount": "100000",
		"currency": "IDR",
		"source_account_id": "00000000-0000-0000-0000-000000000002",
		"destination_account_id": "00000000-0000-0000-0000-000000000003",
		"entries": [
			{"account_id": "00000000-0000-0000-0000-000000000002", "direction": "debit", "amount": "100000"},
			{"account_id": "00000000-0000-0000-0000-000000000003", "direction": "credit", "amount": "100000"}
		],
		"external_ref": "ext-ref-123",
		"occurred_at": "2026-07-11T10:30:00Z"
	}`
	assert.JSONEq(t, want, string(b))
}

func TestTransactionPosted_GoldenJSON_NilSourceDest_OmitsExternalRef(t *testing.T) {
	// Reversal's shape: nil Source/Destination, empty ExternalRef.
	txID := uuid.MustParse("00000000-0000-0000-0000-000000000009")
	acc := uuid.MustParse("00000000-0000-0000-0000-00000000000a")

	ev := NewTransactionPosted(
		txID, "reversal", "5000", "IDR", nil, nil,
		[]EntrySummary{{AccountID: acc, Direction: "credit", Amount: "5000"}},
		"",
		fixedTime(),
		nil, nil, "", nil,
	)

	b, err := json.Marshal(ev)
	require.NoError(t, err)

	want := `{
		"schema_version": 1,
		"event_id": "2575546f-be18-5025-803f-3872c4d0e194",
		"tx_id": "00000000-0000-0000-0000-000000000009",
		"transaction_type": "reversal",
		"amount": "5000",
		"currency": "IDR",
		"entries": [{"account_id": "00000000-0000-0000-0000-00000000000a", "direction": "credit", "amount": "5000"}],
		"occurred_at": "2026-07-11T10:30:00Z"
	}`
	assert.JSONEq(t, want, string(b))

	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	_, hasSource := m["source_account_id"]
	_, hasDest := m["destination_account_id"]
	_, hasRef := m["external_ref"]
	_, hasUserID := m["user_id"]
	_, hasTargetUserID := m["target_user_id"]
	assert.False(t, hasSource, "nil Source must be omitted, not null")
	assert.False(t, hasDest, "nil Destination must be omitted, not null")
	assert.False(t, hasRef, "empty ExternalRef must be omitted")
	assert.False(t, hasUserID, "nil UserID must be omitted, not null")
	assert.False(t, hasTargetUserID, "nil TargetUserID must be omitted, not null")
}

// TestTransactionPosted_GoldenJSON_WithUserAndTargetUser proves the
// docs/roadmap/archive/25 Task T4 addition — an OPTIONAL, non-breaking field on an
// existing event type, still SchemaVersion 1 — appears in the wire format
// exactly as services/gateway/internal/notification's consumer expects, for a two-party
// transaction (transfer_p2p shape: both UserID and TargetUserID set).
func TestTransactionPosted_GoldenJSON_WithUserAndTargetUser(t *testing.T) {
	txID := uuid.MustParse("00000000-0000-0000-0000-000000000030")
	acc := uuid.MustParse("00000000-0000-0000-0000-000000000031")
	userID := uuid.MustParse("00000000-0000-0000-0000-000000000040")
	targetUserID := uuid.MustParse("00000000-0000-0000-0000-000000000050")

	ev := NewTransactionPosted(
		txID, "transfer_p2p", "10000", "IDR", nil, nil,
		[]EntrySummary{{AccountID: acc, Direction: "debit", Amount: "10000"}},
		"",
		fixedTime(),
		&userID, &targetUserID, "", nil,
	)

	b, err := json.Marshal(ev)
	require.NoError(t, err)

	want := `{
		"schema_version": 1,
		"event_id": "b12367d5-21ac-5cad-ad4a-a52fa4d7affa",
		"tx_id": "00000000-0000-0000-0000-000000000030",
		"transaction_type": "transfer_p2p",
		"amount": "10000",
		"currency": "IDR",
		"entries": [{"account_id": "00000000-0000-0000-0000-000000000031", "direction": "debit", "amount": "10000"}],
		"occurred_at": "2026-07-11T10:30:00Z",
		"user_id": "00000000-0000-0000-0000-000000000040",
		"target_user_id": "00000000-0000-0000-0000-000000000050"
	}`
	assert.JSONEq(t, want, string(b))
	assert.Equal(t, 1, ev.SchemaVersion, "an optional field addition must never bump SchemaVersion")
}

// TestTransactionPosted_GoldenJSON_WithMerchantTenantID proves Plan 57 T5's
// addition — the same OPTIONAL, non-breaking field pattern as UserID/
// TargetUserID above, still SchemaVersion 1.
func TestTransactionPosted_GoldenJSON_WithMerchantTenantID(t *testing.T) {
	txID := uuid.MustParse("00000000-0000-0000-0000-000000000060")
	acc := uuid.MustParse("00000000-0000-0000-0000-000000000061")
	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000070")

	ev := NewTransactionPosted(
		txID, "merchant_transfer", "25000", "IDR", nil, nil,
		[]EntrySummary{{AccountID: acc, Direction: "debit", Amount: "25000"}},
		"",
		fixedTime(),
		nil, nil, "", &tenantID,
	)

	b, err := json.Marshal(ev)
	require.NoError(t, err)

	want := `{
		"schema_version": 1,
		"event_id": "7aeb9eae-4c6f-52c1-bc0d-194f0aafa677",
		"tx_id": "00000000-0000-0000-0000-000000000060",
		"transaction_type": "merchant_transfer",
		"amount": "25000",
		"currency": "IDR",
		"entries": [{"account_id": "00000000-0000-0000-0000-000000000061", "direction": "debit", "amount": "25000"}],
		"occurred_at": "2026-07-11T10:30:00Z",
		"merchant_tenant_id": "00000000-0000-0000-0000-000000000070"
	}`
	assert.JSONEq(t, want, string(b))
	assert.Equal(t, 1, ev.SchemaVersion, "an optional field addition must never bump SchemaVersion")
}

// TestTransactionPosted_ExistingEventShape_UnaffectedByMerchantField is the
// T5 "compatibility fixture" — proves a pre-existing transaction type
// (transfer_p2p, no merchant party at all) produces BYTE-IDENTICAL JSON to
// before this change: no merchant_tenant_id key appears at all when the
// field is nil, so no existing consumer sees a shape change.
func TestTransactionPosted_ExistingEventShape_UnaffectedByMerchantField(t *testing.T) {
	txID := uuid.MustParse("00000000-0000-0000-0000-000000000080")
	acc := uuid.MustParse("00000000-0000-0000-0000-000000000081")
	userID := uuid.MustParse("00000000-0000-0000-0000-000000000082")

	ev := NewTransactionPosted(
		txID, "transfer_p2p", "1000", "IDR", nil, nil,
		[]EntrySummary{{AccountID: acc, Direction: "debit", Amount: "1000"}},
		"",
		fixedTime(),
		&userID, nil, "", nil,
	)

	b, err := json.Marshal(ev)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	_, hasMerchantTenantID := m["merchant_tenant_id"]
	assert.False(t, hasMerchantTenantID, "a nil MerchantTenantID must be omitted entirely, not present as null")
}

// TestTransactionPosted_RolloutCompatibility_NewProducerOldConsumer proves
// T5's "rollout test": a producer emitting the NEW merchant_tenant_id
// field must still decode cleanly into an OLDER consumer's struct that has
// never heard of that field — encoding/json ignores unknown keys by
// default, so this is the concrete proof that assumption holds for this
// exact payload shape, not just a general Go claim.
func TestTransactionPosted_RolloutCompatibility_NewProducerOldConsumer(t *testing.T) {
	txID := uuid.New()
	acc := uuid.New()
	tenantID := uuid.New()

	newEvent := NewTransactionPosted(
		txID, "merchant_transfer", "500", "IDR", nil, nil,
		[]EntrySummary{{AccountID: acc, Direction: "debit", Amount: "500"}},
		"",
		fixedTime(),
		nil, nil, "", &tenantID,
	)
	wire, err := json.Marshal(newEvent)
	require.NoError(t, err)

	// oldConsumerShape mirrors TransactionPosted as it looked BEFORE T5 —
	// no MerchantTenantID field at all.
	type oldConsumerShape struct {
		SchemaVersion   int            `json:"schema_version"`
		TxID            uuid.UUID      `json:"tx_id"`
		TransactionType string         `json:"transaction_type"`
		Amount          string         `json:"amount"`
		Currency        string         `json:"currency"`
		Entries         []EntrySummary `json:"entries"`
	}
	var decoded oldConsumerShape
	require.NoError(t, json.Unmarshal(wire, &decoded))
	assert.Equal(t, txID, decoded.TxID)
	assert.Equal(t, "merchant_transfer", decoded.TransactionType)
	assert.Equal(t, "500", decoded.Amount)
}

func TestTransactionReversed_GoldenJSON(t *testing.T) {
	reversalTxID := uuid.MustParse("00000000-0000-0000-0000-000000000010")
	originalTxID := uuid.MustParse("00000000-0000-0000-0000-000000000020")

	ev := NewTransactionReversed(reversalTxID, originalTxID, "5000", "IDR", fixedTime())

	b, err := json.Marshal(ev)
	require.NoError(t, err)

	want := `{
		"schema_version": 1,
		"event_id": "896fa0d4-9d98-5547-84fb-25c600b0c3e0",
		"reversal_tx_id": "00000000-0000-0000-0000-000000000010",
		"original_tx_id": "00000000-0000-0000-0000-000000000020",
		"amount": "5000",
		"currency": "IDR",
		"occurred_at": "2026-07-11T10:30:00Z"
	}`
	assert.JSONEq(t, want, string(b))
}

func TestToPayload_RoundTripsThroughJSON(t *testing.T) {
	txID := uuid.New()
	ev := NewTransactionPosted(txID, "money_in", "100", "IDR", nil, nil, nil, "", fixedTime(), nil, nil, "", nil)

	payload := ev.ToPayload()

	assert.Equal(t, float64(1), payload["schema_version"], "JSON round-trip decodes numbers as float64")
	assert.Equal(t, txID.String(), payload["tx_id"])
	assert.Equal(t, "100", payload["amount"], "amount must stay a string through the round-trip, never a JSON number")
}

func TestTypeConstants_AreVersioned(t *testing.T) {
	assert.Equal(t, "ledger.transaction.posted.v1", TypeTransactionPosted)
	assert.Equal(t, "ledger.transaction.reversed.v1", TypeTransactionReversed)
	assert.Equal(t, "ledger.dispute.lifecycle.v1", TypeDisputeLifecycle)
}

func TestDisputeLifecycle_StableLogicalIDAndValidation(t *testing.T) {
	disputeID := uuid.MustParse("00000000-0000-7000-8000-000000000101")
	originalTxID := uuid.MustParse("00000000-0000-7000-8000-000000000102")
	userID := uuid.MustParse("00000000-0000-7000-8000-000000000103")
	due := fixedTime().Add(24 * time.Hour)

	first := NewDisputeLifecycle(disputeID, originalTxID, &userID, "dp-1", "visa", "10.4", "125000", "IDR", "open", "evidence_submitted", &due, fixedTime())
	second := NewDisputeLifecycle(disputeID, originalTxID, &userID, "dp-1", "visa", "10.4", "125000", "IDR", "open", "evidence_submitted", &due, fixedTime().Add(time.Minute))
	require.NotNil(t, first.EventID)
	require.NotNil(t, second.EventID)
	assert.Equal(t, *first.EventID, *second.EventID, "relay retries must preserve one logical event ID")
	assert.NoError(t, first.Validate())

	mutated := first
	mutated.Amount = "125000.00"
	assert.Error(t, mutated.Validate())
	mutated = first
	mutated.ToStatus = "open"
	mutated.FromStatus = "won"
	assert.Error(t, mutated.Validate())
	mutated = first
	nilRecipient := uuid.Nil
	mutated.RecipientUserID = &nilRecipient
	assert.Error(t, mutated.Validate())
	mutated = first
	mutated.RecipientUserID = nil
	assert.NoError(t, mutated.Validate(), "system-only disputes may have no user recipient")
}

func TestKnownVersionValidationRejectsMalformedFields(t *testing.T) {
	event := NewTransactionPosted(uuid.New(), "money_in", "100", "IDR", nil, nil, []EntrySummary{
		{AccountID: uuid.New(), Direction: "credit", Amount: "100"},
	}, "", fixedTime(), nil, nil, "", nil)
	require.NoError(t, event.Validate())

	for name, mutate := range map[string]func(*TransactionPosted){
		"missing schema version": func(e *TransactionPosted) { e.SchemaVersion = 0 },
		"missing transaction id": func(e *TransactionPosted) { e.TxID = uuid.Nil },
		"invalid amount":         func(e *TransactionPosted) { e.Amount = "1.00" },
		"invalid currency":       func(e *TransactionPosted) { e.Currency = "idr" },
		"invalid entry":          func(e *TransactionPosted) { e.Entries[0].Direction = "unknown" },
		"missing timestamp":      func(e *TransactionPosted) { e.OccurredAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := event
			mutate(&candidate)
			assert.Error(t, candidate.Validate())
		})
	}
}

func TestKnownVersionValidationToleratesOptionalFields(t *testing.T) {
	event := NewTransactionPosted(uuid.New(), "money_in", "100", "IDR", nil, nil, []EntrySummary{
		{AccountID: uuid.New(), Direction: "credit", Amount: "100"},
	}, "", fixedTime(), nil, nil, "", nil)
	body, err := json.Marshal(map[string]any{
		"schema_version":   event.SchemaVersion,
		"tx_id":            event.TxID,
		"transaction_type": event.TransactionType,
		"amount":           event.Amount,
		"currency":         event.Currency,
		"entries":          event.Entries,
		"occurred_at":      event.OccurredAt,
		"future_optional":  map[string]any{"enabled": true},
	})
	require.NoError(t, err)
	var decoded TransactionPosted
	require.NoError(t, json.Unmarshal(body, &decoded))
	assert.NoError(t, decoded.Validate())
}
