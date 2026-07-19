package fraud

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	fraudScreeningEventWriteFailures = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "fraud", Name: "screening_event_write_failures_total",
		Help: "Synchronous screening event writes that entered the spill queue.",
	})
	fraudScreeningEventSpillDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "fraud", Name: "screening_event_spill_depth",
		Help: "Current number of screening events waiting in the bounded spill queue.",
	})
	fraudScreeningEventsLost = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "fraud", Name: "screening_events_lost_total",
		Help: "Screening events dropped because the bounded spill queue overflowed.",
	})
)
