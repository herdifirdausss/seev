package contracts_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

type inventory struct {
	HTTP              []httpSurface `yaml:"http"`
	LeafRegistrations string        `yaml:"leaf_registrations_file"`
	RPC               []rpcSurface  `yaml:"rpc"`
	Events            []event       `yaml:"events"`
}

type httpSurface struct {
	ID            string   `yaml:"id"`
	Service       string   `yaml:"service"`
	Method        string   `yaml:"method"`
	Path          string   `yaml:"path"`
	Kind          string   `yaml:"kind"`
	Owner         string   `yaml:"owner"`
	BehaviorOwner string   `yaml:"behavior_owner"`
	Audience      string   `yaml:"audience"`
	Artifact      string   `yaml:"artifact"`
	Auth          string   `yaml:"auth"`
	Consumers     []string `yaml:"consumers"`
	Lifecycle     string   `yaml:"lifecycle"`
	ContractVer   string   `yaml:"contract_version"`
	Source        string   `yaml:"source"`
}

type rpcSurface struct {
	ID        string   `yaml:"id"`
	Owner     string   `yaml:"owner"`
	Package   string   `yaml:"package"`
	Service   string   `yaml:"service"`
	Version   string   `yaml:"version"`
	Audience  string   `yaml:"audience"`
	Artifact  string   `yaml:"artifact"`
	Consumers []string `yaml:"consumers"`
	Methods   []string `yaml:"methods"`
}

type event struct {
	ID             string   `yaml:"id"`
	Owner          string   `yaml:"owner"`
	RoutingKey     string   `yaml:"routing_key"`
	SchemaVersion  string   `yaml:"schema_version"`
	Artifact       string   `yaml:"artifact"`
	Producer       string   `yaml:"producer"`
	Consumers      []string `yaml:"consumers"`
	Queues         []string `yaml:"queues"`
	Delivery       string   `yaml:"delivery"`
	Ordering       string   `yaml:"ordering"`
	Deduplication  string   `yaml:"deduplication_key"`
	Classification string   `yaml:"data_classification"`
	Lifecycle      string   `yaml:"lifecycle"`
}

type leafInventory struct {
	Registrations []leafRegistration `yaml:"registrations"`
}

type leafRegistration struct {
	Service  string `yaml:"service"`
	Listener string `yaml:"listener"`
	Method   string `yaml:"method"`
	Pattern  string `yaml:"pattern"`
	Owner    string `yaml:"owner"`
	Audience string `yaml:"audience"`
	Source   string `yaml:"source"`
}

func TestSurfaceInventoryIsCompleteAndSafe(t *testing.T) {
	root := filepath.Join(".")
	b, err := os.ReadFile(filepath.Join(root, "surfaces.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var got inventory
	if err := yaml.Unmarshal(b, &got); err != nil {
		t.Fatalf("parse surfaces.yaml: %v", err)
	}

	ids := map[string]string{}
	allowedAudience := map[string]bool{"public": true, "vendor": true, "admin": true, "internal": true, "browser": true, "operational": true}
	allowedKind := map[string]bool{"operation": true, "mount": true, "browser": true, "operational": true}
	unsafe := regexp.MustCompile(`(?i)(password|secret|token|bearer|@|personal|real[-_ ]?data)`)
	for _, h := range got.HTTP {
		if h.ID == "" || h.Service == "" || h.Method == "" || h.Path == "" || h.Owner == "" || h.BehaviorOwner == "" || h.Source == "" {
			t.Errorf("incomplete HTTP surface: %#v", h)
		}
		if !allowedAudience[h.Audience] {
			t.Errorf("HTTP %s has unknown audience %q", h.ID, h.Audience)
		}
		if !allowedKind[h.Kind] {
			t.Errorf("HTTP %s has unknown kind %q", h.ID, h.Kind)
		}
		if len(h.Consumers) == 0 {
			t.Errorf("HTTP %s has no consumers", h.ID)
		}
		if h.Lifecycle != "active" {
			t.Errorf("T0 baseline HTTP %s must be active, got %q", h.ID, h.Lifecycle)
		}
		if unsafe.MatchString(h.ID + " " + h.Path + " " + h.Source) {
			t.Errorf("HTTP %s contains a sensitive-looking inventory value", h.ID)
		}
		if previous, exists := ids[h.ID]; exists {
			t.Errorf("duplicate surface ID %q (%s and %s)", h.ID, previous, h.Source)
		}
		ids[h.ID] = h.Source
	}
	for _, r := range got.RPC {
		if r.ID == "" || r.Owner == "" || r.Package == "" || r.Service == "" || r.Version == "" || r.Artifact == "" || len(r.Methods) == 0 || len(r.Consumers) == 0 {
			t.Errorf("incomplete RPC surface: %#v", r)
		}
		if previous, exists := ids[r.ID]; exists {
			t.Errorf("duplicate surface ID %q (%s and %s)", r.ID, previous, r.Artifact)
		}
		ids[r.ID] = r.Artifact
	}
	for _, e := range got.Events {
		if e.ID == "" || e.ID != e.RoutingKey || e.Owner == "" || e.Producer == "" || e.Artifact == "" || len(e.Consumers) == 0 || len(e.Queues) == 0 || e.Delivery != "at-least-once" || e.Lifecycle != "active" {
			t.Errorf("incomplete event surface: %#v", e)
		}
		if previous, exists := ids[e.ID]; exists {
			t.Errorf("duplicate surface ID %q (%s and %s)", e.ID, previous, e.Artifact)
		}
		ids[e.ID] = e.Artifact
	}
	if got.LeafRegistrations == "" {
		t.Fatal("leaf_registrations_file is required")
	}
	leafPath := filepath.Join(filepath.Dir("surfaces.yaml"), filepath.Base(got.LeafRegistrations))
	leafBytes, err := os.ReadFile(leafPath)
	if err != nil {
		t.Fatalf("read leaf registration inventory %s: %v", got.LeafRegistrations, err)
	}
	var leaves leafInventory
	if err := yaml.Unmarshal(leafBytes, &leaves); err != nil {
		t.Fatalf("parse leaf registration inventory: %v", err)
	}
	if len(leaves.Registrations) == 0 {
		t.Fatal("leaf registration inventory is empty")
	}
	for i, leaf := range leaves.Registrations {
		if leaf.Service == "" || leaf.Listener == "" || leaf.Method == "" || leaf.Pattern == "" || leaf.Owner == "" || leaf.Audience == "" || leaf.Source == "" {
			t.Errorf("incomplete leaf registration %d: %#v", i, leaf)
		}
		if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(leaf.Source))); err != nil {
			t.Errorf("leaf registration %s %s points to missing source %s: %v", leaf.Method, leaf.Pattern, leaf.Source, err)
		}
	}
}

func TestRPCInventoryMatchesProtoSources(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile("surfaces.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var got inventory
	if err := yaml.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}

	serviceRE := regexp.MustCompile(`(?m)^service\s+(\w+)\s*\{([\s\S]*?)^\}`)
	rpcRE := regexp.MustCompile(`(?m)^\s*rpc\s+(\w+)\s*\(`)
	for _, surface := range got.RPC {
		protoPath := filepath.Join(root, filepath.FromSlash(surface.Artifact))
		proto, err := os.ReadFile(protoPath)
		if err != nil {
			t.Fatalf("read %s: %v", surface.Artifact, err)
		}
		matches := serviceRE.FindSubmatch(proto)
		if len(matches) != 3 || string(matches[1]) != surface.Service {
			t.Fatalf("%s: service inventory mismatch: want %s", surface.Artifact, surface.Service)
		}
		var methods []string
		for _, match := range rpcRE.FindAllSubmatch(matches[2], -1) {
			methods = append(methods, string(match[1]))
		}
		sort.Strings(methods)
		want := append([]string(nil), surface.Methods...)
		sort.Strings(want)
		if len(methods) != len(want) {
			t.Fatalf("%s: RPC count mismatch: source=%v inventory=%v", surface.ID, methods, want)
		}
		for i := range methods {
			if methods[i] != want[i] {
				t.Fatalf("%s: RPC mismatch: source=%v inventory=%v", surface.ID, methods, want)
			}
		}
	}
}

func TestEventInventoryMatchesRoutingKeyConstants(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "internal", "ledger", "events", "events.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ledger.transaction.posted.v1",
		"ledger.transaction.reversed.v1",
		"ledger.adjustment.decided.v1",
	} {
		if !regexp.MustCompile(regexp.QuoteMeta(`"` + want + `"`)).Match(b) {
			t.Errorf("routing key %q is missing from events.go", want)
		}
	}
}
