// Package drverify implements docs/roadmap/active/50 T4 (K8-K9): an offline,
// read-only integrity verifier that runs against explicit DSNs for the
// eight restored authoritative databases. It is an operational tool, not
// a deployed service — every connection is read-only, bounded, and
// statement-timeout-limited; drverify never writes to a restored
// database and never imports a domain service's internals, only the
// dependency-free DTOs in internal/assurance/rules (K9's explicit
// allowance).
package drverify

import (
	"sort"
	"sync"
	"time"
)

// Severity is the three-tier classification K9 requires — deliberately
// not assurance's own medium/high/critical scale, which answers a
// different question ("how urgent is this to a human") than drverify's
// ("can traffic safely resume").
type Severity string

const (
	// SeverityFatal blocks traffic: missing/dirty state, an unbalanced
	// ledger, a broken money-movement invariant, or an impossible
	// reference. The gate never passes with a fatal finding present.
	SeverityFatal Severity = "fatal"
	// SeverityRecoverable is a valid in-flight state (a pending pay-in, a
	// held payout, a retryable vendor command) that traffic can safely
	// resume alongside, as long as it is surfaced with a recovery owner
	// rather than silently ignored.
	SeverityRecoverable Severity = "recoverable"
	// SeverityInformational never blocks anything and never needs an
	// owner — stale notifications, a missing request_id on an old
	// record, a run stuck mid-scan pre-backup.
	SeverityInformational Severity = "informational"
)

// Finding is one verification result. Code is a stable, grep-able
// SCREAMING_SNAKE_CASE identifier — never renamed once shipped, since
// runbooks and drill history reference codes directly.
type Finding struct {
	Code       string            `json:"code"`
	Severity   Severity          `json:"severity"`
	Service    string            `json:"service"`
	ResourceID string            `json:"resource_id,omitempty"`
	Message    string            `json:"message"`
	Evidence   map[string]string `json:"evidence,omitempty"`
}

// Summary is a count-only line item for a class of expected, non-finding
// state (K9: recoverable in-flight sagas "must be listed with counts and
// recovery owner, not treated as clean or silently ignored" — this is
// how that requirement is satisfied for states that are not themselves
// wrong, so are not Findings at all).
type Summary struct {
	Service string `json:"service"`
	Metric  string `json:"metric"`
	Count   int    `json:"count"`
	Owner   string `json:"owner,omitempty"`
}

// Report is drverify's entire output — the single JSON document written
// to stdout. Findings and Summaries are always sorted into a stable,
// deterministic order (see sortReport) so two runs against identical
// data produce byte-identical output.
type Report struct {
	mu sync.Mutex `json:"-"`

	GeneratedAt time.Time `json:"generated_at"`
	Findings    []Finding `json:"findings"`
	Summaries   []Summary `json:"summaries"`

	FatalCount         int `json:"fatal_count"`
	RecoverableCount   int `json:"recoverable_count"`
	InformationalCount int `json:"informational_count"`

	// ProjectionOnlyMismatch is true when every ledger-integrity finding
	// is LEDGER_PROJECTION_INCONSISTENT and none is
	// LEDGER_UNBALANCED_TRANSACTION — the one case where re-running
	// scripts/rebuild-projection.sh and then drverify again is the
	// correct next step (T4 Work item 5). drverify itself never invokes
	// the rebuild — it stays read-only; this field is the signal an
	// operator or scripts/dr-drill.sh acts on.
	ProjectionOnlyMismatch bool `json:"projection_only_mismatch"`

	// Errors are check-execution failures (a database unreachable, a
	// query failing outright) — distinct from Findings, which are
	// verification RESULTS about the data. An Error always makes the
	// overall gate fail-closed exactly like a fatal Finding would (see
	// Report.Passed), but is reported separately so an operator does not
	// mistake "the tool broke" for "the data is broken."
	Errors []string `json:"errors,omitempty"`
}

// Passed is the gate drverify exists to answer: no fatal findings, and
// nothing broke while checking. Never true when Errors is non-empty —
// an unrunnable check is exactly the "fail-closed on doubt" case K9's
// whole design exists to prevent bypassing.
func (r *Report) Passed() bool {
	return r.FatalCount == 0 && len(r.Errors) == 0
}

// addFinding/addSummary/addError are the only mutation points on Report
// — every check function (payin.go, payout.go, ...) may run concurrently
// with the others (runner.go bounds them via an errgroup), so all three
// are mutex-protected rather than assuming single-goroutine access.
func (r *Report) addFinding(f Finding) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Findings = append(r.Findings, f)
	switch f.Severity {
	case SeverityFatal:
		r.FatalCount++
	case SeverityRecoverable:
		r.RecoverableCount++
	case SeverityInformational:
		r.InformationalCount++
	}
}

func (r *Report) addSummary(s Summary) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Summaries = append(r.Summaries, s)
}

func (r *Report) addError(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Errors = append(r.Errors, msg)
}

// finalize sorts Findings/Summaries into a stable, deterministic order
// (required test: "result ordering and JSON output are deterministic")
// and computes ProjectionOnlyMismatch. Called exactly once, after every
// check has finished — never call addFinding/addSummary/addError after
// finalize.
func (r *Report) finalize() {
	sort.Slice(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.ResourceID < b.ResourceID
	})
	sort.Slice(r.Summaries, func(i, j int) bool {
		a, b := r.Summaries[i], r.Summaries[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.Metric < b.Metric
	})
	sort.Strings(r.Errors)

	hasUnbalanced, hasProjection := false, false
	for _, f := range r.Findings {
		switch f.Code {
		case "LEDGER_UNBALANCED_TRANSACTION":
			hasUnbalanced = true
		case "LEDGER_PROJECTION_INCONSISTENT":
			hasProjection = true
		}
	}
	r.ProjectionOnlyMismatch = hasProjection && !hasUnbalanced
}
