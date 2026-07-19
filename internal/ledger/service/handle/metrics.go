package ledger

// Package-level metrics: registered exactly once at package init regardless
// of how many times New() constructs a Service, so no explicit Registerer
// needs to be threaded through the constructor (docs/plan/05 Task 1b.6).

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	transactionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ledger",
		Name:      "transactions_total",
		Help:      "Total ledger transactions processed via Handle, by type and outcome (posted|rejected|error). rejected = valid business/input rejection, not a system fault; see apperror.IsBusinessRejection.",
	}, []string{"type", "status"})

	postDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "ledger",
		Name:      "post_duration_seconds",
		Help:      "End-to-end Handle() latency, by transaction type.",
		Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"type"})
)
