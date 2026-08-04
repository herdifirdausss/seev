package database

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// StatsProvider is the small surface needed by the load observer. It keeps
// metric tests independent of a live PostgreSQL server.
type StatsProvider interface{ Stats() sql.DBStats }

var poolMetricNames = []string{
	"seev_database_pool_open_connections",
	"seev_database_pool_in_use_connections",
	"seev_database_pool_idle_connections",
	"seev_database_pool_max_open_connections",
	"seev_database_pool_wait_count_total",
	"seev_database_pool_wait_duration_seconds_total",
	"seev_database_pool_max_idle_closed_total",
	"seev_database_pool_max_idle_time_closed_total",
	"seev_database_pool_max_lifetime_closed_total",
}

type poolCollector struct {
	owner string
	stats StatsProvider
	desc  map[string]*prometheus.Desc
}

// RegisterPoolMetrics registers one bounded-label collector for one service.
// The registerer owns duplicate detection; callers must not use database name,
// host, DSN, query, or request identifiers as labels.
func RegisterPoolMetrics(reg prometheus.Registerer, owner string, stats StatsProvider) error {
	if reg == nil || stats == nil {
		return fmt.Errorf("database pool metrics require registerer and stats provider")
	}
	if !allowedPoolOwners[owner] {
		return fmt.Errorf("unregistered pool metric owner %q", owner)
	}
	c := &poolCollector{owner: owner, stats: stats, desc: make(map[string]*prometheus.Desc, len(poolMetricNames))}
	for _, name := range poolMetricNames {
		c.desc[name] = prometheus.NewDesc(name, "PostgreSQL database/sql pool statistic.", []string{"owner"}, nil)
	}
	return reg.Register(c)
}

var allowedPoolOwners = map[string]bool{
	"gateway": true, "auth": true, "ledger": true, "payin": true,
	"payout": true, "fraud": true, "admin-bff": true, "assurance": true, "vendor": true,
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, name := range poolMetricNames {
		ch <- c.desc[name]
	}
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	stats := c.stats.Stats()
	values := map[string]float64{
		"seev_database_pool_open_connections":            float64(stats.OpenConnections),
		"seev_database_pool_in_use_connections":          float64(stats.InUse),
		"seev_database_pool_idle_connections":            float64(stats.Idle),
		"seev_database_pool_max_open_connections":        float64(stats.MaxOpenConnections),
		"seev_database_pool_wait_count_total":            float64(stats.WaitCount),
		"seev_database_pool_wait_duration_seconds_total": stats.WaitDuration.Seconds(),
		"seev_database_pool_max_idle_closed_total":       float64(stats.MaxIdleClosed),
		"seev_database_pool_max_idle_time_closed_total":  float64(stats.MaxIdleTimeClosed),
		"seev_database_pool_max_lifetime_closed_total":   float64(stats.MaxLifetimeClosed),
	}
	for _, name := range poolMetricNames {
		typeValue := prometheus.GaugeValue
		if strings.HasSuffix(name, "_total") {
			typeValue = prometheus.CounterValue
		}
		ch <- prometheus.MustNewConstMetric(c.desc[name], typeValue, values[name], c.owner)
	}
}

func poolOwner(databaseName string) string {
	name := strings.TrimPrefix(databaseName, "seev_")
	name = strings.TrimPrefix(name, "load_")
	if allowedPoolOwners[name] {
		return name
	}
	return ""
}
