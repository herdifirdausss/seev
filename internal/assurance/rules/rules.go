// Package rules contains pure product-assurance invariants. It has no
// database, network, or domain-service dependency so every branch remains
// table-testable and deterministic.
package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Finding struct {
	Fingerprint string
	Severity    string
	RuleCode    string
	ResourceID  string
	AmountMinor int64
	Currency    string
	Evidence    map[string]string
}

type LedgerProof struct {
	ID                  string
	Type                string
	Status              string
	AmountMinor         int64
	Currency            string
	Gateway             string
	ExternalRef         string
	OriginalReferenceID string
	LifecycleCloserID   string
	BookedFeeMinor      int64
	BookedFeeGateway    string
}

type PayinRecord struct {
	ID               string
	RecordType       string
	Status           string
	UserID           string
	AmountMinor      int64
	Currency         string
	Vendor           string
	Reference        string
	ExternalRef      string
	SettledEventID   string
	RequestIDPresent bool
	Age              time.Duration
	Ledger           []LedgerProof
	SettledWebhook   *PayinRecord
	ConsistencyDelay time.Duration
}

func EvaluatePayin(record PayinRecord) []Finding {
	findings := make([]Finding, 0, 2)
	if !record.RequestIDPresent {
		findings = append(findings, newFinding("PA-CORR", "medium", record, 0, map[string]string{"resource_type": record.RecordType, "reason": "request_id_missing"}))
	}
	success := matchingMoneyIn(record)
	if record.RecordType == "webhook_event" {
		switch record.Status {
		case "posted":
			if len(success) != 1 {
				reason := "ledger_proof_missing"
				if len(success) > 1 {
					reason = "duplicate_ledger_posting"
				} else if hasMoneyIn(record.Ledger) {
					reason = "ledger_proof_mismatch"
				}
				findings = append(findings, newFinding("PA01", "critical", record, record.AmountMinor, map[string]string{"reason": reason, "record_type": "webhook_event"}))
			}
		case "blocked", "failed":
			if hasMoneyIn(record.Ledger) {
				findings = append(findings, newFinding("PA04", "critical", record, record.AmountMinor, map[string]string{"reason": "blocked_or_failed_has_posted_ledger"}))
			}
		}
	}
	if record.RecordType == "intent" {
		if record.Status == "settled" {
			if record.SettledEventID == "" || record.SettledWebhook == nil {
				findings = append(findings, newFinding("PA02", "critical", record, record.AmountMinor, map[string]string{"reason": "settled_without_posted_webhook"}))
			} else if !samePayin(record, *record.SettledWebhook) || record.SettledWebhook.Status != "posted" {
				findings = append(findings, newFinding("PA02", "critical", record, record.AmountMinor, map[string]string{"reason": "intent_webhook_mismatch"}))
			}
		}
		if record.Status == "pending" && record.Age > maxDuration(record.ConsistencyDelay, 2*time.Minute) && len(success) > 0 {
			findings = append(findings, newFinding("PA03", "high", record, record.AmountMinor, map[string]string{"reason": "ledger_posted_intent_pending"}))
		}
	}
	return findings
}

func matchingMoneyIn(record PayinRecord) []LedgerProof {
	matching := make([]LedgerProof, 0, len(record.Ledger))
	for _, proof := range record.Ledger {
		if proof.Type == "money_in" && proof.Status == "posted" && proof.AmountMinor == record.AmountMinor && proof.Currency == record.Currency && proof.ExternalRef == record.ExternalRef && proof.Gateway == record.Vendor {
			matching = append(matching, proof)
		}
	}
	return matching
}

func hasMoneyIn(proofs []LedgerProof) bool {
	for _, proof := range proofs {
		if proof.Type == "money_in" && proof.Status == "posted" {
			return true
		}
	}
	return false
}

func samePayin(intent, event PayinRecord) bool {
	return intent.SettledEventID == event.ID && intent.UserID == event.UserID && intent.AmountMinor == event.AmountMinor && intent.Currency == event.Currency && intent.Reference == event.Reference
}

type VendorCall struct {
	Attempt int
	Vendor  string
	Outcome string
	At      time.Time
}

type VendorCommand struct {
	ID      string
	Vendor  string
	Attempt int
	Status  string
}

type FeeProof struct {
	Exists          bool
	ConsumedByRef   string
	AmountMinor     int64
	Gateway         string
	TransactionType string
}

type PayoutRecord struct {
	ID               string
	Status           string
	AmountMinor      int64
	Currency         string
	Vendor           string
	HoldTxID         string
	SettleTxID       string
	FeeQuoteID       string
	FeeAmountMinor   int64
	FeeGateway       string
	Age              time.Duration
	RequestIDPresent bool
	Hold             *LedgerProof
	Closing          *LedgerProof
	VendorCalls      []VendorCall
	VendorCommands   []VendorCommand
	FeeQuote         *FeeProof
	BookedFeeMinor   int64
	BookedFeeGateway string
}

func EvaluatePayout(record PayoutRecord) []Finding {
	findings := make([]Finding, 0, 3)
	if !record.RequestIDPresent {
		findings = append(findings, newFinding("PO-CORR", "medium", record, 0, map[string]string{"reason": "request_id_missing"}))
	}
	terminal := record.Status == "settled" || record.Status == "cancelled" || record.Status == "failed" || record.Status == "rejected"
	if record.Status != "created" && record.Status != "rejected" && !holdMatches(record) {
		findings = append(findings, newFinding("PO01", "critical", record, record.AmountMinor, map[string]string{"reason": "required_hold_missing_or_mismatch"}))
	}
	if record.Status == "settled" && !closingMatches(record, "withdraw_settle") {
		findings = append(findings, newFinding("PO02", "critical", record, record.AmountMinor, map[string]string{"reason": "settled_without_settle_closer"}))
	}
	if record.Status == "cancelled" && !closingMatches(record, "withdraw_cancel") {
		findings = append(findings, newFinding("PO02", "critical", record, record.AmountMinor, map[string]string{"reason": "cancelled_without_cancel_closer"}))
	}
	if record.Status == "rejected" && record.HoldTxID != "" {
		findings = append(findings, newFinding("PO03", "critical", record, record.AmountMinor, map[string]string{"reason": "rejected_has_hold"}))
	}
	if record.Status == "failed" && holdMatches(record) && !closingMatches(record, "withdraw_cancel") {
		findings = append(findings, newFinding("PO03", "critical", record, record.AmountMinor, map[string]string{"reason": "failed_hold_left_open"}))
	}
	liveCommands := 0
	deadCommand := false
	for _, command := range record.VendorCommands {
		if command.Status == "pending" || command.Status == "processing" || command.Status == "failed" {
			liveCommands++
		}
		if command.Status == "dead" {
			deadCommand = true
		}
	}
	if terminal && liveCommands > 0 {
		findings = append(findings, newFinding("PO04", "critical", record, record.AmountMinor, map[string]string{"reason": "terminal_has_live_vendor_command"}))
	} else if liveCommands > 1 {
		findings = append(findings, newFinding("PO04", "critical", record, record.AmountMinor, map[string]string{"reason": "multiple_live_vendor_commands"}))
	}
	if (record.Status == "submitted" || record.Status == "vendor_pending") && (record.Age > 15*time.Minute || deadCommand) {
		findings = append(findings, newFinding("PO05", "high", record, record.AmountMinor, map[string]string{"reason": "vendor_command_stuck_or_dead"}))
	}
	if vendorChangedAfterAcceptance(record.VendorCalls) {
		findings = append(findings, newFinding("PO06", "critical", record, record.AmountMinor, map[string]string{"reason": "vendor_changed_after_accepted_or_uncertain"}))
	}
	if record.FeeQuote != nil && record.FeeQuote.Exists {
		if record.FeeQuote.ConsumedByRef != "payout:"+record.ID || record.FeeQuote.AmountMinor != record.FeeAmountMinor || record.FeeQuote.Gateway != record.FeeGateway || (record.FeeAmountMinor > 0 && (record.BookedFeeMinor != record.FeeAmountMinor || record.BookedFeeGateway != record.FeeGateway)) {
			findings = append(findings, newFinding("PO07", "critical", record, record.AmountMinor, map[string]string{"reason": "fee_quote_or_booked_fee_mismatch"}))
		}
	}
	return findings
}

func holdMatches(record PayoutRecord) bool {
	return record.HoldTxID != "" && record.Hold != nil && record.Hold.ID == record.HoldTxID && record.Hold.Type == "withdraw_initiate" && record.Hold.Status == "posted" && record.Hold.AmountMinor == record.AmountMinor && record.Hold.Currency == record.Currency
}

func closingMatches(record PayoutRecord, expectedType string) bool {
	if record.SettleTxID == "" || record.Closing == nil {
		return false
	}
	return record.Closing.ID == record.SettleTxID && record.Closing.Type == expectedType && record.Closing.Status == "posted" && record.Closing.OriginalReferenceID == record.HoldTxID
}

func vendorChangedAfterAcceptance(calls []VendorCall) bool {
	acceptedVendor := ""
	for _, call := range calls {
		if call.Outcome == "accepted" || call.Outcome == "uncertain" {
			if acceptedVendor == "" {
				acceptedVendor = call.Vendor
			}
			if call.Vendor != acceptedVendor {
				return true
			}
		}
		if acceptedVendor != "" && call.Vendor != acceptedVendor {
			return true
		}
	}
	return false
}

func newFinding(ruleCode, severity string, record any, amount int64, evidence map[string]string) Finding {
	resourceID := ""
	currency := ""
	switch value := record.(type) {
	case PayinRecord:
		resourceID, currency = value.ID, value.Currency
	case PayoutRecord:
		resourceID, currency = value.ID, value.Currency
	}
	canonical := strings.Join([]string{ruleCode, resourceID, strconv.FormatInt(amount, 10), currency}, "|")
	digest := sha256.Sum256([]byte(canonical))
	return Finding{Fingerprint: hex.EncodeToString(digest[:]), Severity: severity, RuleCode: ruleCode, ResourceID: resourceID, AmountMinor: amount, Currency: currency, Evidence: evidence}
}

func maxDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func ParseMinor(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("minor amount %q: %w", value, err)
	}
	return parsed, nil
}
