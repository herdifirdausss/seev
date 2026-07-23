package drverify

import (
	"fmt"

	"github.com/herdifirdausss/seev/internal/assurance/rules"
)

// classifyAssuranceFinding maps an internal/assurance/rules.Finding
// (medium/high/critical — a "how urgent for a human" scale) onto
// drverify's fatal/recoverable/informational scale (K9 — "can traffic
// safely resume"). These answer different questions, so the mapping is
// by rule-code family, not a blanket severity translation:
//   - critical rules (PA01/PA02/PA04, PO01/PO02/PO03/PO04/PO06/PO07) are
//     exactly K9's fatal examples: missing/contradictory ledger proof,
//     duplicate settlement, an invalid lifecycle closer, a fee-proof
//     mismatch — fatal.
//   - high rules (PA03, PO05) describe a stale-but-recoverable in-flight
//     state — a pending pay-in past its consistency delay, a vendor
//     command stuck or dead — exactly K9's "recoverable... retryable
//     vendor commands" language.
//   - medium rules (PA-CORR, PO-CORR) flag a missing request_id: a data-
//     hygiene gap with no money-safety implication — informational.
func classifyAssuranceFinding(f rules.Finding, service string) Finding {
	code, severity := "", SeverityInformational
	switch f.RuleCode {
	case "PA01", "PA02", "PA04":
		code, severity = "PAYIN_LEDGER_PROOF_INVALID", SeverityFatal
	case "PA03":
		code, severity = "PAYIN_SETTLEMENT_STALE", SeverityRecoverable
	case "PA-CORR":
		code, severity = "PAYIN_CORRELATION_GAP", SeverityInformational
	case "PO01", "PO02", "PO03", "PO04", "PO06", "PO07":
		code, severity = "PAYOUT_LIFECYCLE_INVALID", SeverityFatal
	case "PO05":
		code, severity = "PAYOUT_VENDOR_COMMAND_STUCK", SeverityRecoverable
	case "PO-CORR":
		code, severity = "PAYOUT_CORRELATION_GAP", SeverityInformational
	default:
		code = "UNCLASSIFIED_ASSURANCE_FINDING"
	}
	evidence := make(map[string]string, len(f.Evidence)+1)
	for k, v := range f.Evidence {
		evidence[k] = v
	}
	evidence["assurance_rule_code"] = f.RuleCode
	return Finding{
		Code: code, Severity: severity, Service: service, ResourceID: f.ResourceID,
		Message:  fmt.Sprintf("%s: %s", f.RuleCode, evidence["reason"]),
		Evidence: evidence,
	}
}
