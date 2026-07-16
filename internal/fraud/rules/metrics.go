package rules

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var screeningTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "fraud",
	Subsystem: "screening",
	Name:      "findings_total",
	Help:      "Total fraud screening findings by rule and verdict.",
}, []string{"rule", "verdict"})
