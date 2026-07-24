package retentionworker

// docs/roadmap/active/51-a8-data-lifecycle-privacy.md K13's fixed, low-cardinality metric
// set — package-level so it registers once regardless of how many Runners
// a process constructs (mirrors internal/payout/worker's own convention).
// Labels are owner/class/action/result only: never a user ID, table
// primary key, or free-text reason.

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var runsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "seev",
	Subsystem: "retention",
	Name:      "runs_total",
	Help:      "Retention class runs, by owner, action, and result (docs/roadmap/active/51 K13).",
}, []string{"owner", "action", "result"})

var rowsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "seev",
	Subsystem: "retention",
	Name:      "rows_total",
	Help:      "Rows purged or redacted, by owner, class, and action (docs/roadmap/active/51 K13). Dry runs never increment this.",
}, []string{"owner", "class", "action"})

var holdsGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "seev",
	Subsystem: "retention",
	Name:      "holds",
	Help:      "Current retention hold count, by owner, scope, and status (docs/roadmap/active/51 K13). Refreshed once per RunOnce call.",
}, []string{"owner", "scope", "status"})
