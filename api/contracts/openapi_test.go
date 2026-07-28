package contracts_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPISourcesAreDeterministicContractInputs(t *testing.T) {
	root := filepath.Join("..", "openapi")
	files := []string{"public-v1.yaml", "webhooks-v1.yaml", "admin-v1.yaml", "internal-v1.yaml"}
	operationIDs := map[string]string{}
	for _, name := range files {
		path := filepath.Join(root, name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(body, &doc); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if doc["openapi"] != "3.1.0" {
			t.Errorf("%s must declare OpenAPI 3.1.0", name)
		}
		paths, ok := doc["paths"].(map[string]any)
		if !ok || len(paths) == 0 {
			t.Fatalf("%s has no paths", name)
		}
		for route, raw := range paths {
			item, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("%s %s is not a path item", name, route)
			}
			for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"} {
				rawOperation, exists := item[method]
				if !exists {
					continue
				}
				op, ok := rawOperation.(map[string]any)
				if !ok {
					t.Fatalf("%s %s %s is not an operation", name, method, route)
				}
				id, _ := op["operationId"].(string)
				if id == "" {
					t.Errorf("%s %s %s has no operationId", name, method, route)
				}
				if previous, exists := operationIDs[id]; exists {
					t.Errorf("duplicate operationId %q in %s and %s", id, previous, name)
				}
				operationIDs[id] = name
				for _, extension := range []string{"x-see-owner", "x-see-audience"} {
					if value, exists := op[extension].(string); !exists || value == "" {
						t.Errorf("%s %s %s is missing %s", name, method, route, extension)
					}
				}
				if _, exists := op["responses"]; !exists {
					t.Errorf("%s %s %s has no responses", name, method, route)
				}
			}
		}
		checkRefs(t, path, body)
	}
	if len(operationIDs) < 30 {
		t.Fatalf("source contract set contains only %d operations; expected the current baseline", len(operationIDs))
	}
}

func TestHTTPInventoryAndOpenAPIOperationsAreBidirectionallyCovered(t *testing.T) {
	root := filepath.Join("..", "..")
	body, err := os.ReadFile("surfaces.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		HTTP []struct {
			Method   string `yaml:"method"`
			Path     string `yaml:"path"`
			Kind     string `yaml:"kind"`
			Artifact string `yaml:"artifact"`
		} `yaml:"http"`
	}
	if err := yaml.Unmarshal(body, &inventory); err != nil {
		t.Fatal(err)
	}

	type operation struct{ method, path, artifact string }
	wanted := map[operation]bool{}
	for _, surface := range inventory.HTTP {
		if surface.Kind != "operation" {
			continue
		}
		if surface.Method == "ANY" || surface.Artifact == "" {
			t.Errorf("business operation %s %s has no concrete method/artifact", surface.Method, surface.Path)
			continue
		}
		wanted[operation{strings.ToLower(surface.Method), surface.Path, surface.Artifact}] = true
	}

	seen := map[operation]bool{}
	for _, name := range []string{"public-v1.yaml", "webhooks-v1.yaml", "admin-v1.yaml", "internal-v1.yaml"} {
		artifact := "api/openapi/" + name
		contractBody, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(artifact)))
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := yaml.Unmarshal(contractBody, &document); err != nil {
			t.Fatalf("parse %s: %v", artifact, err)
		}
		paths, ok := document["paths"].(map[string]any)
		if !ok {
			t.Fatalf("%s has no paths", artifact)
		}
		for path, raw := range paths {
			item, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("%s %s is not a path item", artifact, path)
			}
			for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"} {
				if _, ok := item[method]; !ok {
					continue
				}
				key := operation{method, path, artifact}
				seen[key] = true
				if !wanted[key] {
					t.Errorf("OpenAPI operation %s %s is missing from surfaces.yaml", strings.ToUpper(method), path)
				}
			}
		}
	}
	for entry := range wanted {
		if !seen[entry] {
			t.Errorf("inventory operation %s %s is missing from %s", strings.ToUpper(entry.method), entry.path, entry.artifact)
		}
	}
}

func checkRefs(t *testing.T, sourcePath string, body []byte) {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal(body, &node); err != nil {
		t.Fatal(err)
	}
	var walk func(*yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				key, value := n.Content[i], n.Content[i+1]
				if key.Value == "$ref" && strings.Contains(value.Value, "#") {
					file := strings.SplitN(value.Value, "#", 2)[0]
					if file != "" {
						if _, err := os.Stat(filepath.Join(filepath.Dir(sourcePath), file)); err != nil {
							t.Errorf("%s has unresolved external ref %q: %v", sourcePath, value.Value, err)
						}
					}
				}
				walk(value)
			}
		} else {
			for _, child := range n.Content {
				walk(child)
			}
		}
	}
	walk(&node)
}

func TestErrorRegistryIsUniqueAndBounded(t *testing.T) {
	body, err := os.ReadFile("errors.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Codes []struct {
			Code     string `yaml:"code"`
			Statuses []int  `yaml:"statuses"`
			Owner    string `yaml:"owner"`
			Retry    bool   `yaml:"retryable"`
		} `yaml:"codes"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var codes []string
	for _, code := range doc.Codes {
		if code.Code == "" || code.Owner == "" || len(code.Statuses) == 0 || seen[code.Code] {
			t.Errorf("invalid or duplicate error registry entry: %#v", code)
		}
		seen[code.Code] = true
		codes = append(codes, code.Code)
		for _, status := range code.Statuses {
			if status < 400 || status > 599 {
				t.Errorf("error %s has non-error status %d", code.Code, status)
			}
		}
	}
	sort.Strings(codes)
	if len(codes) == 0 {
		t.Fatal(fmt.Errorf("error registry is empty"))
	}
}

func TestExceptionalHTTPRepresentationsAreExplicit(t *testing.T) {
	common, err := os.ReadFile(filepath.Join("..", "openapi", "components", "common.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	commonText := string(common)
	for _, marker := range []string{"CSV:", "text/csv:", "Binary:", "application/octet-stream:", "NoContent:"} {
		if !strings.Contains(commonText, marker) {
			t.Errorf("shared OpenAPI components omit %q", marker)
		}
	}
	for _, name := range []string{"public-v1.yaml", "admin-v1.yaml"} {
		body, err := os.ReadFile(filepath.Join("..", "openapi", name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !strings.Contains(text, "multipart/form-data:") || !strings.Contains(text, "format: binary") {
			t.Errorf("%s does not explicitly describe multipart binary input", name)
		}
	}
	public, err := os.ReadFile(filepath.Join("..", "openapi", "public-v1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(public), "enum: [json, csv]") || !strings.Contains(string(public), "responses/Statement") {
		t.Error("public statement contract does not expose its JSON/CSV exception")
	}

	inventoryBody, err := os.ReadFile(filepath.Join("surfaces.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		HTTP []struct {
			ID       string `yaml:"id"`
			Kind     string `yaml:"kind"`
			Artifact string `yaml:"artifact"`
		} `yaml:"http"`
	}
	if err := yaml.Unmarshal(inventoryBody, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, surface := range inventory.HTTP {
		if surface.Kind == "browser" && surface.Artifact != "none" {
			t.Errorf("browser HTML surface %s must be explicitly classified with artifact: none", surface.ID)
		}
	}
}

func TestApprovedBreakingChangesAreExplicitAndPlanLinked(t *testing.T) {
	body, err := os.ReadFile("approved-breaking.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		SchemaVersion int `yaml:"schema_version"`
		Changes       []struct {
			Method      string `yaml:"method"`
			Path        string `yaml:"path"`
			Predecessor string `yaml:"predecessor_operation_id"`
			Successor   string `yaml:"successor_operation_id"`
			Reason      string `yaml:"reason"`
			Plan        string `yaml:"plan"`
		} `yaml:"changes"`
	}
	if err := yaml.Unmarshal(body, &file); err != nil {
		t.Fatal(err)
	}
	if file.SchemaVersion != 1 || len(file.Changes) != 1 {
		t.Fatalf("expected one schema-version 1 approved cutover, got %#v", file)
	}
	change := file.Changes[0]
	if change.Method != "POST" || change.Path != "/webhooks/{vendor}" || change.Predecessor == change.Successor || change.Reason == "" || change.Plan == "" {
		t.Fatalf("incomplete approved cutover: %#v", change)
	}
	if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(change.Plan))); err != nil {
		t.Fatalf("approved cutover points to missing plan %s: %v", change.Plan, err)
	}
}
