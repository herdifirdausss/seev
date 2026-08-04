package objectoutbox

// docs/roadmap/archive/51-a8-data-lifecycle-privacy.md K13's metric set for the object-delete
// outbox — package-level so it registers once regardless of how many
// Workers a process constructs, mirroring internal/platform/lifecycle/retention/worker's own
// convention. Labels are owner/ref_table only: never an object key or row
// ID.

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var deletedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "seev",
	Subsystem: "object_outbox",
	Name:      "deleted_total",
	Help:      "Objects successfully deleted from the store and marked done, by owner and ref_table.",
}, []string{"owner", "ref_table"})

var failuresTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "seev",
	Subsystem: "object_outbox",
	Name:      "failures_total",
	Help:      "Object-delete attempts that failed and were left pending for retry, by owner and ref_table.",
}, []string{"owner", "ref_table"})

var lastBatchFailedGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "seev",
	Subsystem: "object_outbox",
	Name:      "last_batch_failed",
	Help:      "Rows that failed in the most recent ProcessOnce batch and remain pending for retry, by owner (K13 policy-lag signal).",
}, []string{"owner"})
