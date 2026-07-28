package loadreport

import (
	"strings"
	"testing"
)

func summary(id string, achieved float64) Summary {
	return Summary{SchemaVersion: 1, RunID: id, ProfileID: "local-small", Workload: "W1", WorkloadVersion: "1", DatasetHash: "sha256:" + strings.Repeat("0", 64), OfferedWUPerSecond: 10, AchievedWUPerSecond: achieved, PercentilesMS: map[string]float64{"p50": 10, "p95": 20, "p99": 30}, IntegrityPassed: true, GatePassed: true, ArtifactHashes: map[string]string{"k6": "sha256:" + strings.Repeat("1", 64)}}
}

func TestAggregateRejectsMixedRunsAndKeepsWorstPercentiles(t *testing.T) {
	got, err := Aggregate([]Summary{summary("b", 9), summary("a", 8)})
	if err != nil {
		t.Fatal(err)
	}
	if got.MedianAchievedWUPerSecond != 8.5 || got.WorstAchievedWUPerSecond != 8 || got.WorstP99MS != 30 {
		t.Fatalf("unexpected aggregate: %#v", got)
	}
	mixed := summary("c", 7)
	mixed.ProfileID = "other"
	if _, err := Aggregate([]Summary{summary("a", 8), mixed}); err == nil {
		t.Fatal("mixed profile accepted")
	}
}

func TestSummaryRejectsPassedIntegrityFailureAndMissingPercentile(t *testing.T) {
	bad := summary("bad", 1)
	bad.IntegrityPassed = false
	if err := bad.Validate(); err == nil {
		t.Fatal("integrity failure accepted")
	}
	bad = summary("bad", 1)
	delete(bad.PercentilesMS, "p99")
	if err := bad.Validate(); err == nil {
		t.Fatal("missing percentile accepted")
	}
}

func TestEvaluateRequiresAndAppliesGateInputs(t *testing.T) {
	s := summary("gate", 10)
	thresholds := Thresholds{SchemaVersion: 1}
	thresholds.Gates.MinAchievedFraction = 0.99
	thresholds.Gates.MaxUnexpectedFailureRate = 0.005
	thresholds.Gates.MaxDroppedIterations = 0
	thresholds.Gates.MaxOldestOutboxAge = 10
	thresholds.Gates.MaxOutboxDrain = 30
	thresholds.Gates.MaxQueueDrain = 60
	thresholds.Gates.MaxMemoryFraction = 0.90
	thresholds.Gates.MaxCPUFraction = 0.85
	thresholds.Gates.MaxPoolWaitFraction = 0.05
	if failures := Evaluate(s, thresholds); len(failures) != 1 || failures[0] != "missing gate inputs" {
		t.Fatalf("expected missing gate inputs, got %v", failures)
	}
	s.GateInputs = &GateInputs{}
	if failures := Evaluate(s, thresholds); len(failures) != 0 {
		t.Fatalf("unexpected gate failures: %v", failures)
	}
	s.GateInputs.CPUFraction = 0.99
	if failures := Evaluate(s, thresholds); len(failures) != 1 || failures[0] != "cpu fraction" {
		t.Fatalf("expected cpu failure, got %v", failures)
	}
}
