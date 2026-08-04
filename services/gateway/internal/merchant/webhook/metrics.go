package webhook

// Plan 57 T9 (K13-style low-cardinality metrics) — package-level so this
// registers once regardless of how many RelayWorkers a process
// constructs, mirroring internal/platform/lifecycle/objectoutbox/internal/platform/lifecycle/retention/worker's own
// convention. The single label is "result" (delivered|failed|dead) —
// never a tenant id, endpoint id, or delivery id.
import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var deliveryAttemptsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "seev",
	Subsystem: "merchant_webhook",
	Name:      "delivery_attempts_total",
	Help:      "Outbound webhook delivery attempts, by result (delivered|failed|dead).",
}, []string{"result"})
