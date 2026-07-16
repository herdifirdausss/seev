package messaging

// metrics.go — Prometheus metrics.
//
// All metrics are registered on a caller-supplied prometheus.Registerer so
// tests can use prometheus.NewRegistry() for complete isolation.
// promauto.With(reg) is used rather than global MustRegister to avoid panics
// on duplicate registration in tests.
//
// New vs v1:
//   - publishDuration label set extended with exchange (multi-exchange support)
//   - activeConsumers gauge: shows live consumer count per queue
//   - topologyDeclaredTotal: tracks how many queues have been declared

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type metrics struct {
	// ── Publisher ────────────────────────────────────────────────────────────
	publishTotal    *prometheus.CounterVec   // labels: routing_key, status
	publishDuration *prometheus.HistogramVec // labels: exchange, routing_key

	// ── Consumer ─────────────────────────────────────────────────────────────
	consumeTotal    *prometheus.CounterVec   // labels: queue, status
	consumeDuration *prometheus.HistogramVec // labels: queue
	activeConsumers *prometheus.GaugeVec     // labels: queue  (live gauge)

	// ── DLQ ──────────────────────────────────────────────────────────────────
	dlqTotal *prometheus.CounterVec // labels: queue, reason

	// ── Connection & Channels ─────────────────────────────────────────────────
	connectionStatus  prometheus.Gauge // 1=up, 0=down
	reconnectTotal    prometheus.Counter
	channelOpenTotal  prometheus.Counter
	channelErrorTotal prometheus.Counter

	// ── Topology ─────────────────────────────────────────────────────────────
	topologyDeclaredTotal prometheus.Counter
}

func newMetrics(reg prometheus.Registerer) *metrics {
	f := promauto.With(reg)

	return &metrics{
		// ── Publisher ──────────────────────────────────────────────────────────
		publishTotal: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: "rabbitmq",
			Name:      "messages_published_total",
			Help:      "Total messages published, by routing_key and status (ok|error|nack).",
		}, []string{"routing_key", "status"}),

		publishDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "rabbitmq",
			Name:      "publish_duration_seconds",
			Help:      "End-to-end publish latency including publisher confirm wait, by exchange and routing_key.",
			// Tuned for fintech: most confirms within 100 ms; p99 under 2.5 s.
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		}, []string{"exchange", "routing_key"}),

		// ── Consumer ────────────────────────────────────────────────────────────
		consumeTotal: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: "rabbitmq",
			Name:      "messages_consumed_total",
			Help:      "Total messages consumed, by queue and status (ok|error|panic|dlq).",
		}, []string{"queue", "status"}),

		consumeDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "rabbitmq",
			Name:      "consume_handler_duration_seconds",
			Help:      "Time spent in the handler per message, by queue.",
			// Wider than publish buckets — handler processing can legitimately
			// take seconds for complex workflows.
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"queue"}),

		activeConsumers: f.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "rabbitmq",
			Name:      "active_consumers",
			Help:      "Number of currently active consumer sessions, by queue.",
		}, []string{"queue"}),

		// ── DLQ ─────────────────────────────────────────────────────────────────
		dlqTotal: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: "rabbitmq",
			Name:      "dlq_messages_total",
			Help:      "Total messages routed to DLQ, by queue and reason (max_attempts|panic|handler_error).",
		}, []string{"queue", "reason"}),

		// ── Connection & Channels ────────────────────────────────────────────────
		connectionStatus: f.NewGauge(prometheus.GaugeOpts{
			Namespace: "rabbitmq",
			Name:      "connection_up",
			Help:      "1 if the AMQP connection is established, 0 otherwise.",
		}),

		reconnectTotal: f.NewCounter(prometheus.CounterOpts{
			Namespace: "rabbitmq",
			Name:      "reconnect_attempts_total",
			Help:      "Total reconnection attempts since process start.",
		}),

		channelOpenTotal: f.NewCounter(prometheus.CounterOpts{
			Namespace: "rabbitmq",
			Name:      "channel_open_total",
			Help:      "Total AMQP channels opened (pool allocations + topology/consumer channels).",
		}),

		channelErrorTotal: f.NewCounter(prometheus.CounterOpts{
			Namespace: "rabbitmq",
			Name:      "channel_error_total",
			Help:      "Total channel open or operation errors (triggers pool discard).",
		}),

		// ── Topology ────────────────────────────────────────────────────────────
		topologyDeclaredTotal: f.NewCounter(prometheus.CounterOpts{
			Namespace: "rabbitmq",
			Name:      "topology_declared_total",
			Help:      "Total successful DeclareTopology calls across all modules.",
		}),
	}
}
