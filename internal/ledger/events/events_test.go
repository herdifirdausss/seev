package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Golden JSON tests lock the wire format (docs/plan/14 Task T3) — a change
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
		nil, nil, "",
	)

	b, err := json.Marshal(ev)
	require.NoError(t, err)

	want := `{
		"schema_version": 1,
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
		nil, nil, "",
	)

	b, err := json.Marshal(ev)
	require.NoError(t, err)

	want := `{
		"schema_version": 1,
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
// docs/plan/25 Task T4 addition — an OPTIONAL, non-breaking field on an
// existing event type, still SchemaVersion 1 — appears in the wire format
// exactly as internal/notify's consumer expects, for a two-party
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
		&userID, &targetUserID, "",
	)

	b, err := json.Marshal(ev)
	require.NoError(t, err)

	want := `{
		"schema_version": 1,
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

func TestTransactionReversed_GoldenJSON(t *testing.T) {
	reversalTxID := uuid.MustParse("00000000-0000-0000-0000-000000000010")
	originalTxID := uuid.MustParse("00000000-0000-0000-0000-000000000020")

	ev := NewTransactionReversed(reversalTxID, originalTxID, "5000", "IDR", fixedTime())

	b, err := json.Marshal(ev)
	require.NoError(t, err)

	want := `{
		"schema_version": 1,
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
	ev := NewTransactionPosted(txID, "money_in", "100", "IDR", nil, nil, nil, "", fixedTime(), nil, nil, "")

	payload := ev.ToPayload()

	assert.Equal(t, float64(1), payload["schema_version"], "JSON round-trip decodes numbers as float64")
	assert.Equal(t, txID.String(), payload["tx_id"])
	assert.Equal(t, "100", payload["amount"], "amount must stay a string through the round-trip, never a JSON number")
}

func TestTypeConstants_AreVersioned(t *testing.T) {
	assert.Equal(t, "ledger.transaction.posted.v1", TypeTransactionPosted)
	assert.Equal(t, "ledger.transaction.reversed.v1", TypeTransactionReversed)
}
