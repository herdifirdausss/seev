package contracts_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEventMutationFixturesAllowOptionalAdditionsAndRejectBreakingChanges(t *testing.T) {
	root := filepath.Join("testdata", "events")
	baseline := readJSONFixture(t, root, "baseline.json")
	additive := readJSONFixture(t, root, "additive-optional.json")
	forbidden := map[string]map[string]any{
		"removed-required":  readJSONFixture(t, root, "removed-required.json"),
		"changed-type":      readJSONFixture(t, root, "changed-type.json"),
		"required-addition": readJSONFixture(t, root, "required-addition.json"),
	}

	if err := validateEventSchemaMutation(baseline, additive); err != nil {
		t.Fatalf("optional event property addition rejected: %v", err)
	}
	for name, candidate := range forbidden {
		if err := validateEventSchemaMutation(baseline, candidate); err == nil {
			t.Errorf("forbidden event mutation %s unexpectedly passed", name)
		}
	}
}

func readJSONFixture(t *testing.T, root, name string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(body, &schema); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return schema
}

func validateEventSchemaMutation(baseline, candidate map[string]any) error {
	baseRequired := stringSet(baseline["required"])
	nextRequired := stringSet(candidate["required"])
	for field := range baseRequired {
		if !nextRequired[field] {
			return fmt.Errorf("required field %q was removed", field)
		}
	}
	for field := range nextRequired {
		if !baseRequired[field] {
			return fmt.Errorf("new required field %q is not additive", field)
		}
	}

	baseProperties := objectMap(baseline["properties"])
	nextProperties := objectMap(candidate["properties"])
	for field, base := range baseProperties {
		next, ok := nextProperties[field]
		if !ok {
			return fmt.Errorf("property %q was removed", field)
		}
		if !reflect.DeepEqual(wireShape(base), wireShape(next)) {
			return fmt.Errorf("wire shape changed for property %q", field)
		}
	}
	return nil
}

func stringSet(value any) map[string]bool {
	set := map[string]bool{}
	for _, item := range value.([]any) {
		if name, ok := item.(string); ok {
			set[name] = true
		}
	}
	return set
}

func objectMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func wireShape(value any) map[string]any {
	property, _ := value.(map[string]any)
	shape := map[string]any{}
	for _, key := range []string{"type", "format", "const", "enum", "nullable"} {
		if value, ok := property[key]; ok {
			shape[key] = value
		}
	}
	return shape
}
