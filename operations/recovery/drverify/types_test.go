package drverify

import (
	"encoding/json"
	"testing"
)

func TestReportPassed(t *testing.T) {
	// *Report, not Report, in every case: Report embeds a sync.Mutex, and
	// copying a struct that contains one (even just building a table-test
	// slice by value) is a real go vet "copylocks" finding, not a style
	// nit — found live via `go vet -tags=integration ./...` failing.
	cases := []struct {
		name   string
		report *Report
		want   bool
	}{
		{"clean", &Report{}, true},
		{"fatal finding blocks", &Report{FatalCount: 1}, false},
		{"recoverable does not block", &Report{RecoverableCount: 3}, true},
		{"informational does not block", &Report{InformationalCount: 5}, true},
		{"an execution error blocks even with zero fatal findings", &Report{Errors: []string{"ledger: connection refused"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.report.Passed(); got != tc.want {
				t.Fatalf("Passed() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReportFinalizeDeterministicOrdering(t *testing.T) {
	build := func() *Report {
		r := &Report{}
		r.addFinding(Finding{Code: "PAYOUT_LIFECYCLE_INVALID", Service: "payout", ResourceID: "b", Severity: SeverityFatal})
		r.addFinding(Finding{Code: "LEDGER_UNBALANCED_TRANSACTION", Service: "ledger", ResourceID: "z", Severity: SeverityFatal})
		r.addFinding(Finding{Code: "LEDGER_UNBALANCED_TRANSACTION", Service: "ledger", ResourceID: "a", Severity: SeverityFatal})
		r.addSummary(Summary{Service: "payout", Metric: "in_flight_requests", Count: 3})
		r.addSummary(Summary{Service: "payin", Metric: "pending_topup_intents", Count: 1})
		r.addError("assurance: timeout")
		r.addError("auth: timeout")
		r.finalize()
		return r
	}
	first, second := build(), build()

	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	// GeneratedAt is not set by build(), so both are zero-value and equal —
	// this isolates the test to Findings/Summaries/Errors ordering, the
	// thing the required test ("result ordering and JSON output are
	// deterministic") actually cares about.
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("two identical builds produced different JSON:\n%s\nvs\n%s", firstJSON, secondJSON)
	}

	wantOrder := []string{"LEDGER_UNBALANCED_TRANSACTION", "LEDGER_UNBALANCED_TRANSACTION", "PAYOUT_LIFECYCLE_INVALID"}
	for i, f := range first.Findings {
		if f.Code != wantOrder[i] {
			t.Fatalf("finding[%d].Code = %q, want %q", i, f.Code, wantOrder[i])
		}
	}
	if first.Findings[0].ResourceID != "a" || first.Findings[1].ResourceID != "z" {
		t.Fatalf("same-code findings not sorted by resource_id: %+v", first.Findings[:2])
	}
}

func TestReportProjectionOnlyMismatch(t *testing.T) {
	cases := []struct {
		name     string
		findings []Finding
		want     bool
	}{
		{"no findings", nil, false},
		{"projection only", []Finding{{Code: "LEDGER_PROJECTION_INCONSISTENT"}}, true},
		{"unbalanced only", []Finding{{Code: "LEDGER_UNBALANCED_TRANSACTION"}}, false},
		{"both present — never auto-rebuild through an unbalanced ledger", []Finding{
			{Code: "LEDGER_PROJECTION_INCONSISTENT"}, {Code: "LEDGER_UNBALANCED_TRANSACTION"},
		}, false},
		{"unrelated finding only", []Finding{{Code: "MIGRATION_DIRTY"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Report{}
			for _, f := range tc.findings {
				r.addFinding(f)
			}
			r.finalize()
			if r.ProjectionOnlyMismatch != tc.want {
				t.Fatalf("ProjectionOnlyMismatch = %v, want %v", r.ProjectionOnlyMismatch, tc.want)
			}
		})
	}
}

func TestReportSeverityCounts(t *testing.T) {
	r := &Report{}
	r.addFinding(Finding{Severity: SeverityFatal})
	r.addFinding(Finding{Severity: SeverityFatal})
	r.addFinding(Finding{Severity: SeverityRecoverable})
	r.addFinding(Finding{Severity: SeverityInformational})
	if r.FatalCount != 2 || r.RecoverableCount != 1 || r.InformationalCount != 1 {
		t.Fatalf("counts = fatal:%d recoverable:%d informational:%d, want 2/1/1", r.FatalCount, r.RecoverableCount, r.InformationalCount)
	}
}
