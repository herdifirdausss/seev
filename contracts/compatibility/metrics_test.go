package contracts_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestContractMetricsRegistryUsesBoundedSafeLabels(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "contracts", "compatibility", "metrics.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Metrics []struct {
			Name          string              `yaml:"name"`
			Labels        []string            `yaml:"labels"`
			AllowedValues map[string][]string `yaml:"allowed_values"`
		} `yaml:"metrics"`
	}
	if err := yaml.Unmarshal(body, &registry); err != nil {
		t.Fatal(err)
	}
	if len(registry.Metrics) < 6 {
		t.Fatalf("contract metrics registry is incomplete: %d entries", len(registry.Metrics))
	}
	labelPattern := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	for _, metric := range registry.Metrics {
		if !regexp.MustCompile(`^seev_[a-z0-9_]+$`).MatchString(metric.Name) || len(metric.Labels) == 0 {
			t.Errorf("invalid metric registry entry: %#v", metric)
		}
		seen := map[string]bool{}
		for _, label := range metric.Labels {
			if !labelPattern.MatchString(label) || seen[label] {
				t.Errorf("metric %s has unsafe/duplicate label %q", metric.Name, label)
			}
			seen[label] = true
			for _, value := range metric.AllowedValues[label] {
				if value == "" || len(value) > 64 || !labelPattern.MatchString(value) && value != "2xx" && value != "3xx" && value != "4xx" && value != "5xx" {
					t.Errorf("metric %s label %s has unsafe value %q", metric.Name, label, value)
				}
			}
		}
	}
}
