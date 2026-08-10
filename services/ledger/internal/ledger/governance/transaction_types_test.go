package governance

import "testing"

func TestPendingAdjustmentTypesAreWorkflowOnly(t *testing.T) {
	types := []string{
		"adjustment_credit", "adjustment_debit",
		"adjustment_suspense_credit", "adjustment_suspense_debit",
		"reversal", "chargeback", "freeze_release", "freeze_confiscate",
		"fx_rebalance_credit", "fx_rebalance_debit",
	}
	for _, transactionType := range types {
		if !IsPendingAdjustmentType(transactionType) {
			t.Errorf("%s must be accepted by the pending-adjustment workflow", transactionType)
		}
		if !IsWorkflowOnlyPostType(transactionType) {
			t.Errorf("%s must be rejected by every direct-post transport", transactionType)
		}
	}
}

func TestFXLegsAreWorkflowOnlyButNotPendingAdjustments(t *testing.T) {
	for _, transactionType := range []string{"fx_out", "fx_in"} {
		if IsPendingAdjustmentType(transactionType) {
			t.Errorf("%s must use the FX workflow, not pending adjustments", transactionType)
		}
		if !IsWorkflowOnlyPostType(transactionType) {
			t.Errorf("%s must be rejected by every direct-post transport", transactionType)
		}
	}
}

func TestOrdinaryAndEmergencyTypesRemainDirectlyClassified(t *testing.T) {
	for _, transactionType := range []string{"money_in", "transfer_p2p", "freeze_initiate"} {
		if IsPendingAdjustmentType(transactionType) || IsWorkflowOnlyPostType(transactionType) {
			t.Errorf("%s must not be classified as workflow-only by this policy", transactionType)
		}
	}
}
