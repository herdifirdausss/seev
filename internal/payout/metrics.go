package payout

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// vendorCommandAttemptsTotal is docs/plan/45 K6's
// payout_vendor_command_attempts_total{outcome} counter — incremented once
// per dispatchOne call (relay.go), the only place a command's vendor-call
// outcome is actually classified. outcome is one of
// model.VendorCall{Accepted,Rejected,Uncertain} — the same fixed,
// low-cardinality enum payout_vendor_calls.outcome already uses, never
// derived from request input (docs/plan/43 K7 cardinality discipline).
var vendorCommandAttemptsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "payout",
	Name:      "vendor_command_attempts_total",
	Help:      "Vendor dispatch command attempts by outcome (docs/plan/45 K6).",
}, []string{"outcome"})
