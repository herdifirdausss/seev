package drreseed

import "time"

// Report is drreseed's output — count-only by design (K11's same "counts
// and timestamps, never values" discipline applies here too: user IDs are
// not secrets, but nothing in this report is ever a token, password, or
// raw Redis value).
type Report struct {
	GeneratedAt time.Time `json:"generated_at"`

	PolicyCountersWritten int `json:"policy_counters_written"`

	// FraudEventsReplayed is every posted-transaction event replayed
	// through fraud.VelocityStore.Record within the active 2h TTL window
	// — one Record call atomically writes both the event's dedup marker
	// and its hour-bucketed counter together (see fraud.go), so these are
	// not two separable counts.
	FraudEventsReplayed int `json:"fraud_events_replayed"`
	// FraudCurrentHourEvents is the subset of the above landing in the
	// CURRENT UTC hour — the only bucket VelocityAnomalyRule.Screen ever
	// actually reads (internal/fraud/rules/velocity_anomaly.go).
	FraudCurrentHourEvents int `json:"fraud_current_hour_events"`

	Errors []string `json:"errors,omitempty"`
}

func (r *Report) Passed() bool {
	return len(r.Errors) == 0
}
