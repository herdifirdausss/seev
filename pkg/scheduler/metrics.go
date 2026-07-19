package scheduler

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// jobSkipsTotal is docs/plan/45 K6's scheduler_job_skips_total{job}
// counter — every job name passed to NewScheduler is a fixed identifier
// from this codebase's own source (e.g. "payout-resume",
// "ledger-accrual"), never derived from request input, so this stays a
// small, low-cardinality label set.
var jobSkipsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "scheduler",
	Name:      "job_skips_total",
	Help:      "Cron job ticks skipped because the distributed lock could not be acquired (docs/plan/45 K6) — e.g. Redis unavailable.",
}, []string{"job"})

// PrometheusMetrics implements Metrics, backing JobSkip with the
// scheduler_job_skips_total counter (docs/plan/45 Task T3/K4 — the
// scheduler's own skip-tick-on-Redis-down behavior is UNCHANGED by this
// track; this only makes that behavior observable). JobStart/JobSuccess/
// JobFail are deliberately no-ops here — this type exists specifically for
// the skip metric this track requires, not as a general-purpose scheduler
// instrumentation pass.
type PrometheusMetrics struct{}

func NewPrometheusMetrics() PrometheusMetrics { return PrometheusMetrics{} }

func (PrometheusMetrics) JobStart(string)                      {}
func (PrometheusMetrics) JobSuccess(string, time.Duration)     {}
func (PrometheusMetrics) JobFail(string, time.Duration, error) {}
func (PrometheusMetrics) JobSkip(name string)                  { jobSkipsTotal.WithLabelValues(name).Inc() }

var _ Metrics = PrometheusMetrics{}
