package database

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestRuntimeCollectorExportsSchedulerMetrics(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	require.NoError(t, registry.Register(newRuntimeCollector()))

	families, err := registry.Gather()
	require.NoError(t, err)

	seen := make(map[string]struct{}, len(families))
	for _, family := range families {
		seen[family.GetName()] = struct{}{}
	}
	for _, spec := range runtimeMetricSpecs {
		_, ok := seen[spec.name]
		require.Truef(t, ok, "runtime metric %q was not collected", spec.name)
	}
}
