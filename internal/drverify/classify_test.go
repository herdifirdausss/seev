package drverify

import (
	"testing"

	"github.com/herdifirdausss/seev/internal/assurance/rules"
)

// TestClassifyAssuranceFinding covers every rule code
// internal/assurance/rules actually emits (PA01-PA04, PA-CORR,
// PO01-PO07, PO-CORR) — a rule code silently falling through to
// UNCLASSIFIED_ASSURANCE_FINDING would mean a real money-safety finding
// stops reaching drverify's fatal/recoverable distinction.
func TestClassifyAssuranceFinding(t *testing.T) {
	cases := []struct {
		ruleCode     string
		wantCode     string
		wantSeverity Severity
	}{
		{"PA01", "PAYIN_LEDGER_PROOF_INVALID", SeverityFatal},
		{"PA02", "PAYIN_LEDGER_PROOF_INVALID", SeverityFatal},
		{"PA04", "PAYIN_LEDGER_PROOF_INVALID", SeverityFatal},
		{"PA03", "PAYIN_SETTLEMENT_STALE", SeverityRecoverable},
		{"PA-CORR", "PAYIN_CORRELATION_GAP", SeverityInformational},
		{"PO01", "PAYOUT_LIFECYCLE_INVALID", SeverityFatal},
		{"PO02", "PAYOUT_LIFECYCLE_INVALID", SeverityFatal},
		{"PO03", "PAYOUT_LIFECYCLE_INVALID", SeverityFatal},
		{"PO04", "PAYOUT_LIFECYCLE_INVALID", SeverityFatal},
		{"PO06", "PAYOUT_LIFECYCLE_INVALID", SeverityFatal},
		{"PO07", "PAYOUT_LIFECYCLE_INVALID", SeverityFatal},
		{"PO05", "PAYOUT_VENDOR_COMMAND_STUCK", SeverityRecoverable},
		{"PO-CORR", "PAYOUT_CORRELATION_GAP", SeverityInformational},
	}
	for _, tc := range cases {
		t.Run(tc.ruleCode, func(t *testing.T) {
			f := classifyAssuranceFinding(rules.Finding{RuleCode: tc.ruleCode, ResourceID: "r1", Evidence: map[string]string{"reason": "x"}}, "payin")
			if f.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", f.Code, tc.wantCode)
			}
			if f.Severity != tc.wantSeverity {
				t.Errorf("Severity = %q, want %q", f.Severity, tc.wantSeverity)
			}
			if f.Evidence["assurance_rule_code"] != tc.ruleCode {
				t.Errorf("evidence assurance_rule_code = %q, want %q", f.Evidence["assurance_rule_code"], tc.ruleCode)
			}
		})
	}
}

func TestClassifyAssuranceFindingUnknownCode(t *testing.T) {
	f := classifyAssuranceFinding(rules.Finding{RuleCode: "ZZ99"}, "payin")
	if f.Code != "UNCLASSIFIED_ASSURANCE_FINDING" {
		t.Fatalf("unknown rule code should not silently disappear, got Code=%q", f.Code)
	}
}

// TestValidInFlightPayoutHasNoFindings is the required test: "a valid
// in-flight payout is reported as recoverable rather than corrupted" —
// EvaluatePayout only emits a Finding when something is structurally
// wrong, so a genuinely valid in-flight state must produce zero findings
// (visibility for in-flight sagas comes from checkPayout's Summary
// counts, never from a Finding — see payout.go).
func TestValidInFlightPayoutHasNoFindings(t *testing.T) {
	record := rules.PayoutRecord{
		ID: "p1", Status: "submitted", AmountMinor: 10000, Currency: "USD",
		HoldTxID: "hold1", RequestIDPresent: true,
		Hold:           &rules.LedgerProof{ID: "hold1", Type: "withdraw_initiate", Status: "posted", AmountMinor: 10000, Currency: "USD"},
		VendorCommands: []rules.VendorCommand{{ID: "c1", Status: "processing"}},
	}
	findings := rules.EvaluatePayout(record)
	if len(findings) != 0 {
		t.Fatalf("valid in-flight payout produced findings, want none: %+v", findings)
	}
}
