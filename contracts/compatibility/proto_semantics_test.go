package contracts_test

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestProtoSemanticsHasOneEntryPerRPC(t *testing.T) {
	body, err := os.ReadFile("proto-semantics.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Rules []struct {
			Service       string `yaml:"service"`
			Method        string `yaml:"method"`
			Auth          string `yaml:"auth"`
			Authorization string `yaml:"authorization"`
			Idempotency   string `yaml:"idempotency"`
			Retryability  string `yaml:"retryability"`
			MoneyUnits    string `yaml:"money_units"`
			ErrorMapping  string `yaml:"error_mapping"`
		} `yaml:"rules"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Rules) != 27 {
		t.Fatalf("expected semantic metadata for 27 RPCs, got %d", len(doc.Rules))
	}
	seen := map[string]bool{}
	for _, rule := range doc.Rules {
		key := rule.Service + "/" + rule.Method
		if rule.Service == "" || rule.Method == "" || rule.Auth == "" || rule.Authorization == "" || rule.Idempotency == "" || rule.Retryability == "" || rule.MoneyUnits == "" || rule.ErrorMapping == "" || seen[key] {
			t.Errorf("incomplete or duplicate semantic rule %q: %#v", key, rule)
		}
		seen[key] = true
	}
}
