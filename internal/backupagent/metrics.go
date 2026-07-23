package backupagent

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// The eight metric names below are docs/roadmap/active/50 K13's exact,
// low-cardinality set. Labels are always one of a small fixed set this
// process itself chooses ("full"/"diff" for type, "ok"/"error" for
// result, "latest"/"pitr" for drill mode) — never a filename, database
// row value, LSN, backup ID, or secret path, per K13's explicit ban on
// exposing backup contents through metric labels.
var (
	backupLastSuccessTimestamp = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "seev_backup_last_success_timestamp_seconds",
		Help: "Unix timestamp of the last successful backup, by type (docs/roadmap/active/50 K13).",
	}, []string{"type"})

	backupDuration = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "seev_backup_duration_seconds",
		Help: "Duration of the most recent backup attempt, by type and result (docs/roadmap/active/50 K13).",
	}, []string{"type", "result"})

	backupSizeBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "seev_backup_size_bytes",
		Help: "Size of the most recent successful backup, by type (docs/roadmap/active/50 K13).",
	}, []string{"type"})

	backupRepositoryCheckTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "seev_backup_repository_check_total",
		Help: "pgBackRest repository check attempts, by result (docs/roadmap/active/50 K13).",
	}, []string{"result"})

	backupWALArchiveAge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "seev_backup_wal_archive_age_seconds",
		Help: "Seconds since PostgreSQL last successfully archived a WAL segment (docs/roadmap/active/50 K13) — crossing the RPO budget means archiving is stuck.",
	})

	backupOldestRestorePoint = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "seev_backup_oldest_restore_point_timestamp_seconds",
		Help: "Unix timestamp of the earliest point-in-time this repository can currently restore to (docs/roadmap/active/50 K13).",
	})

	// drRPO/drRTO are populated by the T6 game-day drill, not by this
	// track — declared here because K13 fixes the full eight-metric set
	// backup-agent exposes, and a metric that only appears after the
	// first drill run would be a silent scrape-time surprise.
	drDrillRPO = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "seev_dr_drill_rpo_seconds",
		Help: "Recovery point objective observed by the most recent DR drill, by mode and result (docs/roadmap/active/50 K12/K13).",
	}, []string{"mode", "result"})

	drDrillRTO = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "seev_dr_drill_rto_seconds",
		Help: "Recovery time objective observed by the most recent DR drill, by mode and result (docs/roadmap/active/50 K12/K13).",
	}, []string{"mode", "result"})
)

// RecordDrillResult publishes the two DR-drill metrics — called by the
// docs/roadmap/active/50 T6 game-day drill script once it has measured both
// boundaries (K12); mode is "latest" or "pitr", result is "ok" or
// "error".
func RecordDrillResult(mode, result string, rpo, rto time.Duration) {
	drDrillRPO.WithLabelValues(mode, result).Set(rpo.Seconds())
	drDrillRTO.WithLabelValues(mode, result).Set(rto.Seconds())
}
