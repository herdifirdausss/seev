package database

import (
	"database/sql"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type statsStub struct{ value sql.DBStats }

func (s statsStub) Stats() sql.DBStats { return s.value }

func TestRegisterPoolMetricsExportsBoundedStats(t *testing.T) {
	registry := prometheus.NewRegistry()
	stats := statsStub{value: sql.DBStats{OpenConnections: 4, InUse: 3, Idle: 1, MaxOpenConnections: 10, WaitCount: 7, WaitDuration: 2 * time.Second, MaxIdleClosed: 2, MaxIdleTimeClosed: 3, MaxLifetimeClosed: 5}}
	if err := RegisterPoolMetrics(registry, "ledger", stats); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "pool_test", ConstLabels: prometheus.Labels{"owner": "ledger"}}, func() float64 { return float64(stats.Stats().InUse) })); got != 3 {
		t.Fatalf("unexpected stub metric %v", got)
	}
	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(metricFamilies) != 9 {
		t.Fatalf("expected 9 pool metrics, got %d", len(metricFamilies))
	}
}

func TestRegisterPoolMetricsRejectsUnknownAndDuplicateOwners(t *testing.T) {
	registry := prometheus.NewRegistry()
	stats := statsStub{}
	if err := RegisterPoolMetrics(registry, "seev_load_ledger", stats); err == nil {
		t.Fatal("database name accepted as metric owner")
	}
	if err := RegisterPoolMetrics(registry, "ledger", stats); err != nil {
		t.Fatal(err)
	}
	if err := RegisterPoolMetrics(registry, "ledger", stats); err == nil {
		t.Fatal("duplicate collector accepted")
	}
}

func TestPoolOwnerNormalizesLoadDatabaseNames(t *testing.T) {
	if poolOwner("seev_load_ledger") != "ledger" || poolOwner("seev_ledger") != "ledger" || poolOwner("other") != "" {
		t.Fatal("pool owner mapping is not bounded")
	}
}
