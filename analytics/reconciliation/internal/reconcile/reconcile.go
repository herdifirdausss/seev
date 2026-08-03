package reconcile

import (
	"time"

	"github.com/herdifirdausss/seev/analytics/reconciliation/internal/core"
)

type Check struct {
	Name           string
	SourceService  string
	Source         string
	WarehouseModel string
	Currency       string
	Expected       int64
	Actual         int64
	Critical       bool
	Details        string
}

type Result struct {
	Checks          []Check
	CriticalFailed  int
	WarningFailed   int
	CompletedStatus string
}

func Evaluate(checks []Check) Result {
	result := Result{Checks: checks, CompletedStatus: "passed"}
	for _, check := range checks {
		if core.Delta(check.Expected, check.Actual) == 0 {
			continue
		}
		if check.Critical {
			result.CriticalFailed++
		} else {
			result.WarningFailed++
		}
	}
	if result.CriticalFailed > 0 || result.WarningFailed > 0 {
		result.CompletedStatus = "failed"
	}
	return result
}

func Cutoff(source, warehouse time.Time) time.Time {
	return core.SafeCutoff(source, warehouse)
}
