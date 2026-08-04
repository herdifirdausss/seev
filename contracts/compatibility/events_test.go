package contracts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/contracts/events/ledger"
	"gopkg.in/yaml.v3"
)

func TestEventCatalogSchemasAndCurrentPayloads(t *testing.T) {
	root := filepath.Join("..", "..")
	catalogBytes, err := os.ReadFile(filepath.Join(root, "contracts", "events", "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Events []struct {
			ID             string           `yaml:"id"`
			RoutingKey     string           `yaml:"routing_key"`
			Schema         string           `yaml:"schema"`
			Lifecycle      string           `yaml:"lifecycle"`
			Classification string           `yaml:"data_classification"`
			Consumers      []map[string]any `yaml:"consumers"`
			Example        map[string]any   `yaml:"example"`
		} `yaml:"events"`
	}
	if err := yaml.Unmarshal(catalogBytes, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Events) != 4 {
		t.Fatalf("expected four current event catalog entries, got %d", len(catalog.Events))
	}

	byID := map[string]map[string]any{
		events.TypeTransactionPosted:   events.NewTransactionPosted(uuid.MustParse("00000000-0000-7000-8000-000000000010"), "money_in", "100", "IDR", nil, nil, []events.EntrySummary{}, "", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), nil, nil, "", nil).ToPayload(),
		events.TypeTransactionReversed: events.NewTransactionReversed(uuid.MustParse("00000000-0000-7000-8000-000000000011"), uuid.MustParse("00000000-0000-7000-8000-000000000012"), "100", "IDR", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)).ToPayload(),
		events.TypeAdjustmentDecided:   events.NewAdjustmentDecided(uuid.MustParse("00000000-0000-7000-8000-000000000013"), "synthetic-requester", "synthetic-approver", "rejected", nil, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)).ToPayload(),
	}
	due := time.Date(2026, 1, 16, 3, 4, 5, 0, time.UTC)
	dispute := events.NewDisputeLifecycle(
		uuid.MustParse("00000000-0000-7000-8000-000000000009"),
		uuid.MustParse("00000000-0000-7000-8000-000000000010"),
		func() *uuid.UUID { id := uuid.MustParse("00000000-0000-7000-8000-000000000011"); return &id }(),
		"synthetic-dispute", "visa", "10.4", "125000", "IDR", "", "open", &due,
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	)
	byID[events.TypeDisputeLifecycle] = dispute.ToPayload()
	for _, entry := range catalog.Events {
		if entry.ID == "" || entry.RoutingKey != entry.ID || entry.Schema == "" || entry.Lifecycle != "active" || entry.Classification == "" || len(entry.Consumers) == 0 || byID[entry.ID] == nil {
			t.Errorf("incomplete or unknown catalog entry: %#v", entry)
			continue
		}
		schemaBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Schema)))
		if err != nil {
			t.Fatalf("read schema %s: %v", entry.Schema, err)
		}
		var schema struct {
			Required []string `json:"required"`
			Title    string   `json:"title"`
		}
		if err := json.Unmarshal(schemaBytes, &schema); err != nil {
			t.Fatalf("parse schema %s: %v", entry.Schema, err)
		}
		if schema.Title != entry.ID || len(schema.Required) == 0 {
			t.Errorf("schema %s title/required fields do not match catalog", entry.Schema)
		}
		payload := byID[entry.ID]
		for _, required := range schema.Required {
			if _, exists := payload[required]; !exists {
				t.Errorf("%s current payload misses required field %q", entry.ID, required)
			}
		}
		if payload["event_id"] == nil {
			t.Errorf("%s current producer payload has no logical event_id", entry.ID)
		}
		encoded, _ := json.Marshal(entry.Example)
		if strings.Contains(strings.ToLower(string(encoded)), "password") || strings.Contains(string(encoded), "@example.com") {
			t.Errorf("%s catalog example looks like it contains sensitive data", entry.ID)
		}
	}
}
