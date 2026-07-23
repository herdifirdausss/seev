package worker

// Package-level metric, registered once regardless of how many times
// NewResumeJob is constructed (mirrors internal/ledger/worker's own
// approach, docs/roadmap/archive/43 K5).

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// stuckRequests is a gauge, not a counter — it reports the CURRENT backlog
// size per status at the last resume tick, not a monotonic total. Every
// status ResumeJob knows about is set every tick (0 included), so an empty
// status never goes stale by simply not being reported.
var stuckRequests = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "payout",
	Name:      "stuck_requests",
	Help:      "Payout requests older than the resume threshold, by status (docs/roadmap/archive/43 K5).",
}, []string{"status"})

// vendorCommandsGauge is docs/roadmap/archive/45 K6's payout_vendor_commands{status}
// gauge — the current backlog per command status, refreshed every
// dispatchGaugeRefreshInterval. status is one of commandStatuses
// (vendor_relay.go) — a fixed, low-cardinality enum, never derived from
// request input.
var vendorCommandsGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "payout",
	Name:      "vendor_commands",
	Help:      "Vendor dispatch commands by status (docs/roadmap/archive/45 K6).",
}, []string{"status"})

// vendorCommandsReapedTotal is docs/roadmap/archive/45 K6's
// payout_vendor_command_reaped_total counter.
var vendorCommandsReapedTotal = promauto.NewCounter(prometheus.CounterOpts{
	Namespace: "payout",
	Name:      "vendor_command_reaped_total",
	Help:      "Vendor dispatch commands reaped from an expired processing lease (docs/roadmap/archive/45 K6).",
})
