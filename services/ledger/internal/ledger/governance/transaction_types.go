// Package governance owns cross-transport transaction authorization policy.
// Keeping these classifications beside the Ledger domain prevents HTTP,
// gRPC, and maker/checker entry points from drifting into different security
// decisions for the same transaction type.
package governance

// IsPendingAdjustmentType reports whether transactionType may be proposed
// through pending_adjustments and executed only after a distinct checker
// approves it.
func IsPendingAdjustmentType(transactionType string) bool {
	switch transactionType {
	case "adjustment_credit",
		"adjustment_debit",
		"adjustment_suspense_credit",
		"adjustment_suspense_debit",
		"reversal",
		"chargeback",
		"freeze_release",
		"freeze_confiscate",
		"fx_rebalance_credit",
		"fx_rebalance_debit":
		return true
	default:
		return false
	}
}

// IsWorkflowOnlyPostType reports whether direct HTTP/gRPC posting must be
// rejected. FX conversion legs use their quote/position workflow rather than
// pending_adjustments, while all other values are maker/checker adjustments.
func IsWorkflowOnlyPostType(transactionType string) bool {
	if IsPendingAdjustmentType(transactionType) {
		return true
	}
	return transactionType == "fx_out" || transactionType == "fx_in"
}
