package assurance

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	runDuration        = prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: "assurance", Name: "run_duration_seconds", Help: "Duration of assurance runs."})
	runFailures        = prometheus.NewCounter(prometheus.CounterOpts{Namespace: "assurance", Name: "run_failures_total", Help: "Assurance runs that failed before cursor advancement."})
	recordsScanned     = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "assurance", Name: "records_scanned_total", Help: "Records read from owner services."}, []string{"source"})
	findingsBySeverity = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "assurance", Name: "findings", Help: "Current finding count by severity and rule."}, []string{"severity", "rule"})
	moneyAtRisk        = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "assurance", Name: "money_at_risk_minor", Help: "Open finding amount in minor units by currency."}, []string{"currency"})
	cursorLag          = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "assurance", Name: "cursor_lag_seconds", Help: "Seconds since each source cursor was persisted."}, []string{"source"})
	alertDeliveries    = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "assurance", Name: "alert_deliveries_total", Help: "Assurance alert delivery attempts."}, []string{"result", "severity"})
)

func init() {
	for _, metric := range []prometheus.Collector{runDuration, runFailures, recordsScanned, findingsBySeverity, moneyAtRisk, cursorLag, alertDeliveries} {
		_ = prometheus.Register(metric)
	}
}

func (m *Module) refreshMetrics(ctx context.Context) error {
	findingsBySeverity.Reset()
	moneyAtRisk.Reset()
	rows, err := m.db.QueryContext(ctx, `SELECT severity, rule_code, COUNT(*) FROM assurance_findings WHERE status IN ('open','acknowledged') GROUP BY severity, rule_code`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var severity, rule string
		var count int64
		if err := rows.Scan(&severity, &rule, &count); err != nil {
			rows.Close()
			return err
		}
		findingsBySeverity.WithLabelValues(severity, rule).Set(float64(count))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	rows, err = m.db.QueryContext(ctx, `SELECT currency, COALESCE(SUM(amount_minor),0) FROM assurance_findings WHERE status IN ('open','acknowledged') GROUP BY currency`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var currency string
		var amount int64
		if err := rows.Scan(&currency, &amount); err != nil {
			rows.Close()
			return err
		}
		moneyAtRisk.WithLabelValues(currency).Set(float64(amount))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	rows, err = m.db.QueryContext(ctx, `SELECT source, EXTRACT(EPOCH FROM (now() - updated_at_service)) FROM assurance_cursors`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var source string
		var lag float64
		if err := rows.Scan(&source, &lag); err != nil {
			rows.Close()
			return err
		}
		cursorLag.WithLabelValues(source).Set(lag)
	}
	return rows.Err()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
