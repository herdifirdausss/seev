// Package metrics owns bounded metrics used to explain B0 saturation.
// These metrics are safe in normal binaries too; labels are validated against
// fixed registries and never contain request, rule, user, or database data.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	resolutionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "seev_resolution_duration_seconds",
		Help: "Duration of fee and payment-routing resolution.",
	}, []string{"owner", "kind", "result"})
	resolutionTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "seev_resolution_total",
		Help: "Total fee and payment-routing resolutions.",
	}, []string{"owner", "kind", "result"})
)

var allowed = map[string]map[string]bool{
	"ledger": {"fee": true},
	"payin":  {"payin_routing": true},
	"payout": {"payout_routing": true},
}

func ObserveResolution(owner, kind, result string, started time.Time) {
	if !allowed[owner][kind] || (result != "success" && result != "not_found" && result != "error") {
		return
	}
	resolutionDuration.WithLabelValues(owner, kind, result).Observe(time.Since(started).Seconds())
	resolutionTotal.WithLabelValues(owner, kind, result).Inc()
}
