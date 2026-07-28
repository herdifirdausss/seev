// Package loadreport validates and aggregates small B0 run summaries. Raw
// time series are intentionally outside this package and outside Git.
package loadreport

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type GateInputs struct {
	MaxOutboxAgeSeconds  float64 `json:"max_outbox_age_seconds"`
	MaxQueueDrainSeconds float64 `json:"max_queue_drain_seconds"`
	MemoryFraction       float64 `json:"memory_fraction"`
	CPUFraction          float64 `json:"cpu_fraction"`
	PoolWaitFraction     float64 `json:"pool_wait_fraction"`
}

type Thresholds struct {
	SchemaVersion int `yaml:"schema_version"`
	Gates         struct {
		MaxUnexpectedFailureRate float64 `yaml:"max_unexpected_failure_rate"`
		MaxDroppedIterations     int64   `yaml:"max_dropped_iterations"`
		MinAchievedFraction      float64 `yaml:"min_achieved_fraction"`
		MaxOldestOutboxAge       float64 `yaml:"max_oldest_outbox_age_seconds"`
		MaxOutboxDrain           float64 `yaml:"max_outbox_drain_seconds"`
		MaxQueueDrain            float64 `yaml:"max_queue_drain_seconds"`
		MaxMemoryFraction        float64 `yaml:"max_memory_fraction"`
		MaxCPUFraction           float64 `yaml:"max_cpu_fraction"`
		MaxPoolWaitFraction      float64 `yaml:"max_pool_wait_fraction"`
	} `yaml:"gates"`
}

type Summary struct {
	SchemaVersion       int                `json:"schema_version"`
	RunID               string             `json:"run_id"`
	ProfileID           string             `json:"profile_id"`
	Workload            string             `json:"workload"`
	WorkloadVersion     string             `json:"workload_version"`
	DatasetHash         string             `json:"dataset_hash"`
	OfferedWUPerSecond  float64            `json:"offered_wu_per_second"`
	AchievedWUPerSecond float64            `json:"achieved_wu_per_second"`
	DroppedIterations   int64              `json:"dropped_iterations"`
	UnexpectedFailures  int64              `json:"unexpected_failures"`
	TotalIterations     int64              `json:"total_iterations"`
	PercentilesMS       map[string]float64 `json:"percentiles_ms"`
	DrainSeconds        float64            `json:"drain_seconds"`
	IntegrityPassed     bool               `json:"integrity_passed"`
	GatePassed          bool               `json:"gate_passed"`
	ArtifactHashes      map[string]string  `json:"artifact_hashes"`
	GateFailures        []string           `json:"gate_failures,omitempty"`
	GateInputs          *GateInputs        `json:"gate_inputs,omitempty"`
}

func LoadThresholds(path string) (Thresholds, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Thresholds{}, err
	}
	var thresholds Thresholds
	if err := yaml.Unmarshal(body, &thresholds); err != nil {
		return Thresholds{}, fmt.Errorf("decode thresholds: %w", err)
	}
	if thresholds.SchemaVersion != 1 {
		return Thresholds{}, fmt.Errorf("unsupported thresholds schema %d", thresholds.SchemaVersion)
	}
	return thresholds, nil
}

func Evaluate(s Summary, thresholds Thresholds) []string {
	failures := append([]string(nil), s.GateFailures...)
	if s.OfferedWUPerSecond > 0 && s.AchievedWUPerSecond/s.OfferedWUPerSecond < thresholds.Gates.MinAchievedFraction {
		failures = append(failures, "achieved fraction")
	}
	if s.TotalIterations > 0 && float64(s.UnexpectedFailures)/float64(s.TotalIterations) > thresholds.Gates.MaxUnexpectedFailureRate {
		failures = append(failures, "unexpected failure rate")
	}
	if s.DroppedIterations > thresholds.Gates.MaxDroppedIterations {
		failures = append(failures, "dropped iterations")
	}
	if s.GateInputs == nil {
		return append(failures, "missing gate inputs")
	}
	if s.GateInputs.MaxOutboxAgeSeconds > thresholds.Gates.MaxOldestOutboxAge {
		failures = append(failures, "oldest outbox age")
	}
	if s.DrainSeconds > thresholds.Gates.MaxOutboxDrain || s.GateInputs.MaxQueueDrainSeconds > thresholds.Gates.MaxQueueDrain {
		failures = append(failures, "drain time")
	}
	if s.GateInputs.MemoryFraction > thresholds.Gates.MaxMemoryFraction {
		failures = append(failures, "memory fraction")
	}
	if s.GateInputs.CPUFraction > thresholds.Gates.MaxCPUFraction {
		failures = append(failures, "cpu fraction")
	}
	if s.GateInputs.PoolWaitFraction > thresholds.Gates.MaxPoolWaitFraction {
		failures = append(failures, "pool wait fraction")
	}
	return failures
}

func LoadSummary(path string) (Summary, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Summary{}, err
	}
	var summary Summary
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&summary); err != nil {
		return Summary{}, fmt.Errorf("decode summary %s: %w", path, err)
	}
	if err := summary.Validate(); err != nil {
		return Summary{}, fmt.Errorf("summary %s: %w", path, err)
	}
	return summary, nil
}

func (s Summary) Validate() error {
	if s.SchemaVersion != 1 || s.RunID == "" || s.ProfileID == "" || s.Workload == "" || s.WorkloadVersion == "" || !strings.HasPrefix(s.DatasetHash, "sha256:") {
		return fmt.Errorf("incomplete summary identity")
	}
	for name, value := range map[string]float64{"offered": s.OfferedWUPerSecond, "achieved": s.AchievedWUPerSecond, "drain": s.DrainSeconds} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("invalid %s value", name)
		}
	}
	if s.TotalIterations < 0 || s.UnexpectedFailures < 0 || s.DroppedIterations < 0 || s.UnexpectedFailures > s.TotalIterations {
		return fmt.Errorf("invalid iteration counts")
	}
	for _, name := range []string{"p50", "p95", "p99"} {
		value, ok := s.PercentilesMS[name]
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("missing or invalid percentile %s", name)
		}
	}
	for name, hash := range s.ArtifactHashes {
		if name == "" || !strings.HasPrefix(hash, "sha256:") {
			return fmt.Errorf("invalid artifact hash %q", name)
		}
	}
	if !s.IntegrityPassed && s.GatePassed {
		return fmt.Errorf("gate cannot pass when integrity failed")
	}
	return nil
}

type AggregateReport struct {
	ProfileID                 string   `json:"profile_id"`
	Workload                  string   `json:"workload"`
	WorkloadVersion           string   `json:"workload_version"`
	DatasetHash               string   `json:"dataset_hash"`
	RunCount                  int      `json:"run_count"`
	WorstAchievedWUPerSecond  float64  `json:"worst_achieved_wu_per_second"`
	MinAchievedWUPerSecond    float64  `json:"min_achieved_wu_per_second"`
	MaxAchievedWUPerSecond    float64  `json:"max_achieved_wu_per_second"`
	MedianAchievedWUPerSecond float64  `json:"median_achieved_wu_per_second"`
	WorstP95MS                float64  `json:"worst_p95_ms"`
	WorstP99MS                float64  `json:"worst_p99_ms"`
	AllPassed                 bool     `json:"all_passed"`
	RunIDs                    []string `json:"run_ids"`
}

func Aggregate(summaries []Summary) (AggregateReport, error) {
	if len(summaries) == 0 {
		return AggregateReport{}, fmt.Errorf("no summaries")
	}
	first := summaries[0]
	values := make([]float64, len(summaries))
	result := AggregateReport{ProfileID: first.ProfileID, Workload: first.Workload, WorkloadVersion: first.WorkloadVersion, DatasetHash: first.DatasetHash, RunCount: len(summaries), AllPassed: true, RunIDs: make([]string, 0, len(summaries))}
	for i, summary := range summaries {
		if err := summary.Validate(); err != nil {
			return AggregateReport{}, err
		}
		if summary.ProfileID != first.ProfileID || summary.Workload != first.Workload || summary.WorkloadVersion != first.WorkloadVersion || summary.DatasetHash != first.DatasetHash {
			return AggregateReport{}, fmt.Errorf("summaries mix profile, workload, version, or dataset")
		}
		values[i] = summary.AchievedWUPerSecond
		result.RunIDs = append(result.RunIDs, summary.RunID)
		if i == 0 || summary.AchievedWUPerSecond < result.MinAchievedWUPerSecond {
			result.MinAchievedWUPerSecond = summary.AchievedWUPerSecond
		}
		if summary.AchievedWUPerSecond > result.MaxAchievedWUPerSecond {
			result.MaxAchievedWUPerSecond = summary.AchievedWUPerSecond
		}
		if summary.AchievedWUPerSecond < result.WorstAchievedWUPerSecond || i == 0 {
			result.WorstAchievedWUPerSecond = summary.AchievedWUPerSecond
		}
		if summary.PercentilesMS["p95"] > result.WorstP95MS {
			result.WorstP95MS = summary.PercentilesMS["p95"]
		}
		if summary.PercentilesMS["p99"] > result.WorstP99MS {
			result.WorstP99MS = summary.PercentilesMS["p99"]
		}
		result.AllPassed = result.AllPassed && summary.GatePassed && summary.IntegrityPassed && summary.DroppedIterations == 0
	}
	sort.Float64s(values)
	if len(values)%2 == 1 {
		result.MedianAchievedWUPerSecond = values[len(values)/2]
	} else {
		result.MedianAchievedWUPerSecond = (values[len(values)/2-1] + values[len(values)/2]) / 2
	}
	sort.Strings(result.RunIDs)
	return result, nil
}

func Markdown(result AggregateReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# B0 load report\n\n- Profile: `%s`\n- Workload: `%s` v`%s`\n- Dataset: `%s`\n- Runs: %d (`%s`)\n- All gates passed: `%t`\n\n", result.ProfileID, result.Workload, result.WorkloadVersion, result.DatasetHash, result.RunCount, strings.Join(result.RunIDs, "`, `"), result.AllPassed)
	b.WriteString("| Metric | Value |\n| --- | ---: |\n")
	fmt.Fprintf(&b, "| Worst achieved WU/s | %.3f |\n| Min achieved WU/s | %.3f |\n| Median achieved WU/s | %.3f |\n| Max achieved WU/s | %.3f |\n| Worst p95 (ms) | %.3f |\n| Worst p99 (ms) | %.3f |\n", result.WorstAchievedWUPerSecond, result.MinAchievedWUPerSecond, result.MedianAchievedWUPerSecond, result.MaxAchievedWUPerSecond, result.WorstP95MS, result.WorstP99MS)
	b.WriteString("\nPercentiles are reported per run; this aggregate intentionally does not average percentiles.\n")
	return b.String()
}
