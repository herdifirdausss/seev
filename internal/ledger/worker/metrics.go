package worker

// Package-level metrics, registered once regardless of how many times
// NewOutboxRelay/NewVerifier are constructed (mirrors service/handle's
// approach — see docs/roadmap/archive/06 Task 1c.1/1c.2).

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	outboxPublishedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "ledger",
		Name:      "outbox_published_total",
		Help:      "Total outbox events successfully published to the broker.",
	})

	outboxPublishFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "ledger",
		Name:      "outbox_publish_failures_total",
		Help:      "Total outbox publish attempts that failed (will retry until max_retries).",
	})

	outboxPendingGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "ledger",
		Name:      "outbox_pending",
		Help:      "Current number of outbox events in status=pending.",
	})

	outboxDeadGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "ledger",
		Name:      "outbox_dead_total",
		Help:      "Current number of outbox events in status=dead (exhausted retries).",
	})

	outboxReapedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "ledger",
		Name:      "outbox_reaped_total",
		Help:      "Total outbox events reset from stuck 'processing' back to 'failed'.",
	})

	verificationDiscrepanciesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ledger",
		Name:      "verification_discrepancies_total",
		Help:      "Total integrity discrepancies found by the verifier, by check name.",
	}, []string{"check"})

	snapshotRowsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "ledger",
		Name:      "balance_snapshot_rows_total",
		Help:      "Total account_balance_snapshots rows written by the daily snapshot job.",
	})

	snapshotMismatchesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "ledger",
		Name:      "balance_snapshot_mismatches_total",
		Help:      "Total snapshot-vs-current-balance mismatches found by the daily snapshot job (docs/roadmap/archive/15 Task T1).",
	})
)
