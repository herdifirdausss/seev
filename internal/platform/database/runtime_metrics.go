package database

import (
	"log/slog"
	"runtime/metrics"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var runtimeMetricsOnce sync.Once

type runtimeMetricSpec struct {
	name        string
	runtime     string
	description string
	counter     bool
}

var runtimeMetricSpecs = []runtimeMetricSpec{
	{
		name:        "seev_runtime_goroutines",
		runtime:     "/sched/goroutines:goroutines",
		description: "Current number of goroutines.",
	},
	{
		name:        "seev_runtime_goroutines_created_total",
		runtime:     "/sched/goroutines-created:goroutines",
		description: "Total number of goroutines created by the process.",
		counter:     true,
	},
	{
		name:        "seev_runtime_goroutines_runnable",
		runtime:     "/sched/goroutines/runnable:goroutines",
		description: "Current number of runnable goroutines.",
	},
	{
		name:        "seev_runtime_goroutines_running",
		runtime:     "/sched/goroutines/running:goroutines",
		description: "Current number of running goroutines.",
	},
	{
		name:        "seev_runtime_goroutines_waiting",
		runtime:     "/sched/goroutines/waiting:goroutines",
		description: "Current number of waiting goroutines.",
	},
	{
		name:        "seev_runtime_threads",
		runtime:     "/sched/threads/total:threads",
		description: "Current number of OS threads known to the runtime.",
	},
}

type runtimeCollector struct {
	samples []metrics.Sample
	descs   []*prometheus.Desc
}

func newRuntimeCollector() *runtimeCollector {
	collector := &runtimeCollector{
		samples: make([]metrics.Sample, len(runtimeMetricSpecs)),
		descs:   make([]*prometheus.Desc, len(runtimeMetricSpecs)),
	}
	for i, spec := range runtimeMetricSpecs {
		collector.samples[i].Name = spec.runtime
		collector.descs[i] = prometheus.NewDesc(spec.name, spec.description, nil, nil)
	}
	return collector
}

func registerRuntimeMetrics() {
	runtimeMetricsOnce.Do(func() {
		if err := prometheus.Register(newRuntimeCollector()); err != nil {
			if _, duplicate := err.(prometheus.AlreadyRegisteredError); !duplicate {
				slog.Warn("runtime metrics unavailable", "error", err)
			}
		}
	})
}

func (c *runtimeCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range c.descs {
		ch <- desc
	}
}

func (c *runtimeCollector) Collect(ch chan<- prometheus.Metric) {
	metrics.Read(c.samples)
	for i, sample := range c.samples {
		if sample.Value.Kind() != metrics.KindUint64 {
			continue
		}
		valueType := prometheus.GaugeValue
		if runtimeMetricSpecs[i].counter {
			valueType = prometheus.CounterValue
		}
		ch <- prometheus.MustNewConstMetric(c.descs[i], valueType, float64(sample.Value.Uint64()))
	}
}
