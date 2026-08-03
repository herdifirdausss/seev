package reconcile

import "testing"

func TestEvaluateCriticalMoneyMismatch(t *testing.T) {
	t.Parallel()
	result := Evaluate([]Check{
		{Name: "ledger_row_count", Expected: 4, Actual: 4, Critical: true},
		{Name: "ledger_amount", Expected: 100, Actual: 99, Critical: true},
		{Name: "freshness", Expected: 0, Actual: 1, Critical: false},
	})
	if result.CompletedStatus != "failed" || result.CriticalFailed != 1 || result.WarningFailed != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}
