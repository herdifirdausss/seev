package vendorgw

// Package-level metric, registered once regardless of how many
// HealthTracker instances are constructed (mirrors internal/ledger/worker's
// own approach, docs/roadmap/archive/43 K5).

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// breakerState reports each vendor's current circuit state: 0 closed,
// 1 half-open, 2 open (docs/roadmap/archive/43 K5). The vendor label is always one of
// HealthTracker's own map keys — populated only by Allow/RecordSuccess/
// RecordFailure calls the payin/payout modules make with vendor names from
// their OWN registries (internal/vendorgw.Registry), never raw request
// input — so this is already a bounded, validated allowlist, not
// arbitrary/unbounded label cardinality.
var breakerState = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "vendorgw",
	Name:      "breaker_state",
	Help:      "Circuit breaker state per vendor: 0=closed, 1=half_open, 2=open.",
}, []string{"vendor"})

func breakerStateValue(s HealthState) float64 {
	switch s {
	case StateHalfOpen:
		return 1
	case StateOpen:
		return 2
	default: // StateClosed
		return 0
	}
}

// breakerBackend reports which backend a DistributedBreaker (docs/roadmap/archive/45
// Task T2/K3) is CURRENTLY serving calls from, per namespace ("payin" |
// "payout" — a fixed, internal enum, never request input): 1 while Redis is
// healthy, 0 while degraded to the local in-process fallback. Set only on
// an actual transition (K3: "log sekali per degrade/recover"), never once
// per call — so this gauge is cheap to scrape and its value is always the
// backend the LAST call actually used.
var breakerBackend = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "vendorgw",
	Name:      "breaker_backend",
	Help:      "Which backend the distributed breaker is currently using: 1=redis, 0=local (docs/roadmap/archive/45 K6).",
}, []string{"namespace", "backend"})
