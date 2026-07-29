// Command contractcheck contains the repository-local portion of the A9
// compatibility gate. External schemas are deliberately not required.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

var names = []string{"public-v1.yaml", "webhooks-v1.yaml", "admin-v1.yaml", "internal-v1.yaml", "b2b-v1.yaml"}

type approvedBreaking struct {
	Method                 string `yaml:"method"`
	Path                   string `yaml:"path"`
	PredecessorOperationID string `yaml:"predecessor_operation_id"`
	SuccessorOperationID   string `yaml:"successor_operation_id"`
	Reason                 string `yaml:"reason"`
	Plan                   string `yaml:"plan"`
}

type approvedBreakingFile struct {
	SchemaVersion int                `yaml:"schema_version"`
	Changes       []approvedBreaking `yaml:"changes"`
}

func main() {
	mode := flag.String("mode", "breaking", "check mode")
	baselineDir := flag.String("baseline-dir", filepath.Join("api", "contracts", "baseline", "openapi"), "directory containing the merge-base bundles")
	currentDir := flag.String("current-dir", filepath.Join("api", "openapi", "dist"), "directory containing the current bundles")
	approvedPath := flag.String("approved-breakings", filepath.Join("api", "contracts", "approved-breaking.yaml"), "explicitly reviewed intentional cutovers")
	mergeBaseRef := flag.String("merge-base-ref", os.Getenv("CONTRACT_MERGE_BASE_REF"), "git merge-base ref containing generated bundles; optional in local bootstrap mode")
	flag.Parse()
	if *mode != "breaking" {
		fmt.Fprintf(os.Stderr, "unsupported contract check mode %q\n", *mode)
		os.Exit(2)
	}
	approved, err := readApprovedBreakings(*approvedPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid approved breaking changes: %v\n", err)
		os.Exit(1)
	}
	for _, name := range names {
		current := filepath.Join(*currentDir, name)
		currentBody, currentErr := os.ReadFile(current)
		baselineBody, baselineErr := readBaseline(*baselineDir, *mergeBaseRef, name)
		if currentErr != nil || baselineErr != nil {
			fmt.Fprintf(os.Stderr, "missing compatibility artifact for %s (current=%v baseline=%v)\n", name, currentErr, baselineErr)
			os.Exit(1)
		}
		if err := compatible(baselineBody, currentBody, approved...); err != nil {
			fmt.Fprintf(os.Stderr, "breaking contract change in %s: %v\n", name, err)
			os.Exit(1)
		}
	}
}

func readApprovedBreakings(path string) ([]approvedBreaking, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file approvedBreakingFile
	if err := yaml.Unmarshal(body, &file); err != nil {
		return nil, err
	}
	if file.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported schema_version %d", file.SchemaVersion)
	}
	seen := map[string]bool{}
	for i := range file.Changes {
		change := &file.Changes[i]
		change.Method = strings.ToUpper(strings.TrimSpace(change.Method))
		key := change.Method + " " + change.Path
		if change.Method == "" || change.Path == "" || change.PredecessorOperationID == "" || change.SuccessorOperationID == "" || change.Reason == "" || change.Plan == "" || seen[key] {
			return nil, fmt.Errorf("invalid or duplicate approved cutover %q", key)
		}
		seen[key] = true
	}
	return file.Changes, nil
}

func readBaseline(directory, ref, name string) ([]byte, error) {
	if ref == "" {
		return os.ReadFile(filepath.Join(directory, name))
	}
	cmd := exec.Command("git", "show", ref+":api/openapi/dist/"+name)
	body, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git merge-base %s: %w", ref, err)
	}
	return body, nil
}

// compatible implements the repository's intentionally conservative HTTP
// policy. Existing operations, parameters, request schemas, response schemas,
// security, and required fields are immutable; new operations, optional
// parameters/properties, and response statuses are additive.
func compatible(baseline, current []byte, approvals ...approvedBreaking) error {
	base, err := decode(baseline)
	if err != nil {
		return fmt.Errorf("invalid baseline: %w", err)
	}
	next, err := decode(current)
	if err != nil {
		return fmt.Errorf("invalid current contract: %w", err)
	}
	basePaths := object(base["paths"])
	nextPaths := object(next["paths"])
	approvedByRoute := map[string]approvedBreaking{}
	for _, approval := range approvals {
		approvedByRoute[strings.ToUpper(approval.Method)+" "+approval.Path] = approval
	}
	for path, raw := range basePaths {
		basePath := object(raw)
		nextPath := object(nextPaths[path])
		for method, opRaw := range basePath {
			if !isMethod(method) {
				continue
			}
			nextOp, ok := nextPath[method]
			if !ok {
				return fmt.Errorf("removed operation %s %s", strings.ToUpper(method), path)
			}
			if approval, ok := approvedByRoute[strings.ToUpper(method)+" "+path]; ok {
				if object(opRaw)["operationId"] != approval.PredecessorOperationID || object(nextOp)["operationId"] != approval.SuccessorOperationID {
					return fmt.Errorf("approved cutover does not match operation IDs for %s %s", strings.ToUpper(method), path)
				}
				delete(approvedByRoute, strings.ToUpper(method)+" "+path)
				continue
			}
			if err := compareOperation(path, method, object(opRaw), object(nextOp)); err != nil {
				return err
			}
		}
	}
	baseComponents := object(object(base["components"])["schemas"])
	nextComponents := object(object(next["components"])["schemas"])
	for name, raw := range baseComponents {
		nextSchema, ok := nextComponents[name]
		if !ok {
			return fmt.Errorf("removed component schema %s", name)
		}
		if err := compareSchema("components.schemas."+name, object(raw), object(nextSchema)); err != nil {
			return err
		}
	}
	return nil
}

func decode(body []byte) (map[string]any, error) {
	var value any
	if err := yaml.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	normalize(value)
	return object(value), nil
}

func normalize(value any) {
	switch v := value.(type) {
	case map[string]any:
		for _, child := range v {
			normalize(child)
		}
	case map[any]any:
		for key, child := range v {
			normalize(child)
			_ = key
		}
	}
}

func object(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	if typed, ok := value.(map[any]any); ok {
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[fmt.Sprint(key)] = child
		}
		return out
	}
	return map[string]any{}
}

func isMethod(name string) bool {
	switch strings.ToLower(name) {
	case "get", "put", "post", "delete", "options", "head", "patch", "trace":
		return true
	}
	return false
}

func compareOperation(path, method string, base, next map[string]any) error {
	if base["operationId"] != next["operationId"] {
		return fmt.Errorf("changed operationId for %s %s", strings.ToUpper(method), path)
	}
	for _, field := range []string{"security", "requestBody"} {
		if field == "requestBody" {
			if err := compareRequestBody(path, method, object(base[field]), object(next[field])); err != nil {
				return err
			}
			continue
		}
		if !reflect.DeepEqual(base[field], next[field]) {
			return fmt.Errorf("changed %s for %s %s", field, strings.ToUpper(method), path)
		}
	}
	if err := compareParameters(path, method, base["parameters"], next["parameters"]); err != nil {
		return err
	}
	baseResponses, nextResponses := object(base["responses"]), object(next["responses"])
	for status, raw := range baseResponses {
		nextResponse, ok := nextResponses[status]
		if !ok {
			return fmt.Errorf("removed response %s from %s %s", status, strings.ToUpper(method), path)
		}
		if err := compareResponse("response "+status+" for "+method+" "+path, object(raw), object(nextResponse)); err != nil {
			return err
		}
	}
	return nil
}

func compareResponse(label string, base, next map[string]any) error {
	if !reflect.DeepEqual(base["headers"], next["headers"]) {
		return fmt.Errorf("changed headers for %s", label)
	}
	return compareContent(label, object(base["content"]), object(next["content"]))
}

func compareRequestBody(path, method string, base, next map[string]any) error {
	if len(base) == 0 {
		return nil
	}
	if len(next) == 0 {
		return fmt.Errorf("removed request body from %s %s", strings.ToUpper(method), path)
	}
	if !reflect.DeepEqual(base["required"], next["required"]) {
		return fmt.Errorf("changed request-body requiredness for %s %s", strings.ToUpper(method), path)
	}
	return compareContent("request body for "+method+" "+path, object(base["content"]), object(next["content"]))
}

// resolvedParam unwraps the bundler's own representation of a resolved
// $ref (cmd/contractgenerate leaves the resolved object nested under a
// literal "$ref" key rather than replacing the node outright) so name/in
// can actually be read. Without this, every $ref-shaped parameter compares
// as name=nil/in=nil, and an operation with two or more of them (the first
// in the repository being this file's own b2b-v1.yaml endpoints) matches
// whichever candidate happens to be seen first instead of its true
// counterpart — silently comparing unrelated parameters against each
// other. A single $ref parameter per operation (the only shape that
// existed before) never revealed this, because there was nothing else for
// it to collide with.
func resolvedParam(m map[string]any) map[string]any {
	if resolved, ok := m["$ref"]; ok {
		return object(resolved)
	}
	return m
}

func compareParameters(path, method string, baseRaw, nextRaw any) error {
	baseList, nextList := list(baseRaw), list(nextRaw)
	for _, raw := range baseList {
		baseParam := resolvedParam(object(raw))
		found := false
		for _, candidate := range nextList {
			nextParam := resolvedParam(object(candidate))
			if baseParam["name"] == nextParam["name"] && baseParam["in"] == nextParam["in"] {
				found = true
				if !reflect.DeepEqual(baseParam, nextParam) {
					return fmt.Errorf("changed parameter %v in %s %s", baseParam["name"], strings.ToUpper(method), path)
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("removed parameter %v from %s %s", baseParam["name"], strings.ToUpper(method), path)
		}
	}
	return nil
}

func compareContent(label string, base, next map[string]any) error {
	for media, raw := range base {
		nextMedia, ok := next[media]
		if !ok {
			return fmt.Errorf("removed content type %s from %s", media, label)
		}
		if !reflect.DeepEqual(object(raw)["schema"], object(nextMedia)["schema"]) {
			return fmt.Errorf("changed schema for %s content %s", label, media)
		}
	}
	return nil
}

func compareSchema(label string, base, next map[string]any) error {
	if !reflect.DeepEqual(base["type"], next["type"]) || !reflect.DeepEqual(base["format"], next["format"]) || !reflect.DeepEqual(base["enum"], next["enum"]) {
		return fmt.Errorf("changed type/format/enum for %s", label)
	}
	baseRequired, nextRequired := stringSet(base["required"]), stringSet(next["required"])
	for field := range baseRequired {
		if !nextRequired[field] {
			return fmt.Errorf("removed required field %s from %s", field, label)
		}
	}
	for field := range nextRequired {
		if !baseRequired[field] {
			return fmt.Errorf("added required field %s to %s", field, label)
		}
	}
	baseProps, nextProps := object(base["properties"]), object(next["properties"])
	for name, raw := range baseProps {
		nextRaw, ok := nextProps[name]
		if !ok {
			return fmt.Errorf("removed property %s from %s", name, label)
		}
		if !reflect.DeepEqual(raw, nextRaw) {
			return fmt.Errorf("changed property %s in %s", name, label)
		}
	}
	return nil
}

func list(value any) []any {
	if value == nil {
		return nil
	}
	if typed, ok := value.([]any); ok {
		return typed
	}
	return nil
}
func stringSet(value any) map[string]bool {
	out := map[string]bool{}
	for _, item := range list(value) {
		out[fmt.Sprint(item)] = true
	}
	return out
}
