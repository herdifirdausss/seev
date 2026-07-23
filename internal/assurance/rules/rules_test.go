package rules

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func postedLedger(id, gateway, external string, amount int64) LedgerProof {
	return LedgerProof{ID: id, Type: "money_in", Status: "posted", AmountMinor: amount, Currency: "IDR", Gateway: gateway, ExternalRef: external}
}

func TestEvaluatePayin(t *testing.T) {
	event := PayinRecord{ID: "event-1", RecordType: "webhook_event", Status: "posted", UserID: "user-1", AmountMinor: 1000, Currency: "IDR", Vendor: "mockvendor", ExternalRef: "ext-1", RequestIDPresent: true}
	tests := []struct {
		name   string
		record PayinRecord
		rule   string
		want   int
	}{
		{name: "posted clean", record: PayinRecord{ID: event.ID, RecordType: event.RecordType, Status: event.Status, UserID: event.UserID, AmountMinor: event.AmountMinor, Currency: event.Currency, Vendor: event.Vendor, ExternalRef: event.ExternalRef, RequestIDPresent: true, Ledger: []LedgerProof{postedLedger("tx-1", "mockvendor", "ext-1", 1000)}}, want: 0},
		{name: "posted missing proof", record: event, rule: "PA01", want: 1},
		{name: "posted duplicate proof", record: PayinRecord{ID: event.ID, RecordType: event.RecordType, Status: event.Status, UserID: event.UserID, AmountMinor: event.AmountMinor, Currency: event.Currency, Vendor: event.Vendor, ExternalRef: event.ExternalRef, RequestIDPresent: true, Ledger: []LedgerProof{postedLedger("tx-1", "mockvendor", "ext-1", 1000), postedLedger("tx-2", "mockvendor", "ext-1", 1000)}}, rule: "PA01", want: 1},
		{name: "blocked has money", record: PayinRecord{ID: event.ID, RecordType: event.RecordType, Status: "blocked", UserID: event.UserID, AmountMinor: event.AmountMinor, Currency: event.Currency, Vendor: event.Vendor, ExternalRef: event.ExternalRef, RequestIDPresent: true, Ledger: []LedgerProof{postedLedger("tx-1", "mockvendor", "ext-1", 1000)}}, rule: "PA04", want: 1},
		{name: "blocked has mismatched money", record: PayinRecord{ID: event.ID, RecordType: event.RecordType, Status: "blocked", UserID: event.UserID, AmountMinor: event.AmountMinor, Currency: event.Currency, Vendor: event.Vendor, ExternalRef: event.ExternalRef, RequestIDPresent: true, Ledger: []LedgerProof{{ID: "tx-1", Type: "money_in", Status: "posted", AmountMinor: 999, Currency: "IDR", Gateway: "other", ExternalRef: "other"}}}, rule: "PA04", want: 1},
		{name: "pending after posting", record: PayinRecord{ID: "intent-1", RecordType: "intent", Status: "pending", AmountMinor: 1000, Currency: "IDR", Vendor: "mockvendor", ExternalRef: "ext-1", RequestIDPresent: true, Age: 3 * time.Minute, Ledger: []LedgerProof{postedLedger("tx-1", "mockvendor", "ext-1", 1000)}}, rule: "PA03", want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings := EvaluatePayin(test.record)
			require.Len(t, findings, test.want)
			if test.rule != "" {
				require.Equal(t, test.rule, findings[0].RuleCode)
			}
		})
	}
}

// TestEvaluatePayinVendorGatewayMapping is docs/roadmap/active/50 T6's regression for a
// real, non-identity payin_vendor_gateways mapping (e.g. the system's own
// default seed, "mockvendor" -> "bca") — record.Vendor stays the raw
// vendor id while the ledger proof's own Gateway is the resolved
// settlement name, and matchingMoneyIn must correlate them anyway.
func TestEvaluatePayinVendorGatewayMapping(t *testing.T) {
	record := PayinRecord{ID: "event-1", RecordType: "webhook_event", Status: "posted", UserID: "user-1", AmountMinor: 1000, Currency: "IDR", Vendor: "mockvendor", ExternalRef: "ext-1", RequestIDPresent: true, Ledger: []LedgerProof{postedLedger("tx-1", "bca", "ext-1", 1000)}}
	require.Empty(t, EvaluatePayin(record))
}

func TestEvaluatePayinSettledCorrelation(t *testing.T) {
	// A webhook_event's own Reference is always blank in real data
	// (payin_webhook_events has no "reference" column) — ExternalRef is
	// the field that must match the intent's own Reference.
	event := PayinRecord{ID: "event-1", RecordType: "webhook_event", Status: "posted", UserID: "user-1", AmountMinor: 1000, Currency: "IDR", ExternalRef: "ref-1"}
	intent := PayinRecord{ID: "intent-1", RecordType: "intent", Status: "settled", UserID: "user-1", AmountMinor: 1000, Currency: "IDR", Reference: "ref-1", SettledEventID: event.ID, SettledWebhook: &event, RequestIDPresent: true}
	require.Empty(t, EvaluatePayin(intent))
	event.AmountMinor = 999
	findings := EvaluatePayin(intent)
	require.Len(t, findings, 1)
	require.Equal(t, "PA02", findings[0].RuleCode)
}

func TestEvaluatePayout(t *testing.T) {
	hold := &LedgerProof{ID: "hold-1", Type: "withdraw_initiate", Status: "posted", AmountMinor: 5000, Currency: "IDR"}
	closing := &LedgerProof{ID: "settle-1", Type: "withdraw_settle", Status: "posted", OriginalReferenceID: "hold-1"}
	base := PayoutRecord{ID: "payout-1", Status: "settled", AmountMinor: 5000, Currency: "IDR", HoldTxID: "hold-1", SettleTxID: "settle-1", RequestIDPresent: true, Hold: hold, Closing: closing}
	require.Empty(t, EvaluatePayout(base))

	findings := EvaluatePayout(PayoutRecord{ID: "payout-2", Status: "settled", AmountMinor: 5000, Currency: "IDR", HoldTxID: "hold-2", SettleTxID: "settle-2", RequestIDPresent: true})
	require.Len(t, findings, 2)
	require.Equal(t, "PO01", findings[0].RuleCode)
	require.Equal(t, "PO02", findings[1].RuleCode)
}

func TestEvaluatePayoutVendorAndFee(t *testing.T) {
	record := PayoutRecord{ID: "payout-1", Status: "vendor_pending", AmountMinor: 5000, Currency: "IDR", Vendor: "vendor-a", RequestIDPresent: true, Age: 16 * time.Minute,
		VendorCalls:    []VendorCall{{Attempt: 1, Vendor: "vendor-a", Outcome: "accepted"}, {Attempt: 2, Vendor: "vendor-b", Outcome: "uncertain"}},
		VendorCommands: []VendorCommand{{ID: "cmd-1", Vendor: "vendor-a", Status: "dead"}},
		FeeAmountMinor: 100, FeeGateway: "fee-a", FeeQuote: &FeeProof{Exists: true, ConsumedByRef: "payout:other", AmountMinor: 90, Gateway: "fee-b"}}
	findings := EvaluatePayout(record)
	got := map[string]bool{}
	for _, finding := range findings {
		got[finding.RuleCode] = true
	}
	for _, rule := range []string{"PO05", "PO06", "PO07"} {
		require.True(t, got[rule], rule)
	}
}

func TestEvaluatePayoutRequiresBookedFee(t *testing.T) {
	record := PayoutRecord{ID: "payout-1", Status: "settled", AmountMinor: 5000, Currency: "IDR", HoldTxID: "hold-1", SettleTxID: "settle-1", RequestIDPresent: true,
		Hold:           &LedgerProof{ID: "hold-1", Type: "withdraw_initiate", Status: "posted", AmountMinor: 5000, Currency: "IDR"},
		Closing:        &LedgerProof{ID: "settle-1", Type: "withdraw_settle", Status: "posted", OriginalReferenceID: "hold-1"},
		FeeAmountMinor: 100, FeeGateway: "platform", BookedFeeMinor: 0, FeeQuote: &FeeProof{Exists: true, ConsumedByRef: "payout:payout-1", AmountMinor: 100, Gateway: "platform"}}
	findings := EvaluatePayout(record)
	require.Len(t, findings, 1)
	require.Equal(t, "PO07", findings[0].RuleCode)
}

func TestFingerprintStableAndMaskedEvidenceShape(t *testing.T) {
	a := EvaluatePayin(PayinRecord{ID: "event-1", RecordType: "webhook_event", Status: "posted", AmountMinor: 100, Currency: "IDR", RequestIDPresent: true})[0]
	b := EvaluatePayin(PayinRecord{ID: "event-1", RecordType: "webhook_event", Status: "posted", AmountMinor: 100, Currency: "IDR", RequestIDPresent: true})[0]
	require.Equal(t, a.Fingerprint, b.Fingerprint)
	require.NotContains(t, a.Evidence, "raw")
}
