package balancev2

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	stateGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "state",
		Help: "Current numeric state marker for the Ledger balance migration.",
	}, []string{"migration"})
	backfillRowsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "backfill_rows_total",
		Help: "Rows observed by the Ledger balance migration backfill.",
	}, []string{"migration", "result"})
	backfillDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "backfill_duration_seconds",
		Help: "Duration of bounded Ledger balance backfill batches.",
	}, []string{"migration", "result"})
	backfillProgress = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "backfill_progress_ratio",
		Help: "Best-effort durable Ledger balance backfill progress ratio.",
	}, []string{"migration"})
	dualWriteTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "dual_write_total",
		Help: "Ledger balance v2 dual-write attempts by result and mode.",
	}, []string{"migration", "result", "mode"})
	dualWriteDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "dual_write_duration_seconds",
		Help: "Ledger balance v2 dual-write duration.",
	}, []string{"migration", "result"})
	shadowReadsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "shadow_reads_total",
		Help: "Sampled Ledger balance shadow comparisons.",
	}, []string{"migration", "result"})
	shadowCompareDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "shadow_compare_duration_seconds",
		Help: "Duration of bounded Ledger balance shadow comparisons.",
	}, []string{"migration", "result"})
	mismatchesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "mismatches_total",
		Help: "Ledger balance migration mismatches by bounded classification and status.",
	}, []string{"migration", "classification", "status"})
	unresolvedMismatches = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "unresolved_mismatches",
		Help: "Current unresolved Ledger balance migration mismatches.",
	}, []string{"migration", "severity"})
	repairsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "repairs_total",
		Help: "Ledger balance migration repairs by type and result.",
	}, []string{"migration", "type", "result"})
	targetReadsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "target_reads_total",
		Help: "Target-primary Ledger balance reads by bounded result.",
	}, []string{"migration", "result"})
	sourceFallbackTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "source_fallback_total",
		Help: "Target-primary Ledger balance source fallbacks by bounded reason.",
	}, []string{"migration", "reason"})
	readPercentageGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "seev", Subsystem: "data_migration", Name: "read_percentage",
		Help: "Current Ledger balance target-read percentage in basis points.",
	}, []string{"migration"})
)

var stateNumbers = map[string]float64{
	"draft": 0, "validated": 1, "target_ready": 2, "backfilling": 3,
	"dual_write_shadow": 4, "shadow_read": 5, "canary_read": 6,
	"ramping_read": 7, "target_primary": 8, "source_write_disabled": 9,
	"observation": 10, "completed": 11, "paused": 12, "rolling_back": 13,
	"rolled_back": 14, "failed": 15, "cancelled_before_write": 16,
}

func observeMigration(migration Migration) {
	stateGauge.WithLabelValues(migration.Name).Set(stateNumbers[migration.State])
	readPercentageGauge.WithLabelValues(migration.Name).Set(float64(migration.ReadPercentageBasisPoints))
}
