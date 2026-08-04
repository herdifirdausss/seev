package contracts_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

type protoField struct {
	name   string
	number int
	typ    string
}

func TestRemovedProtoFieldsRequireReservedNumberAndName(t *testing.T) {
	root := filepath.Join("testdata", "proto")
	baseline, err := os.ReadFile(filepath.Join(root, "removed-field-baseline.proto"))
	if err != nil {
		t.Fatal(err)
	}
	valid, err := os.ReadFile(filepath.Join(root, "removed-field-valid.proto"))
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := os.ReadFile(filepath.Join(root, "removed-field-invalid.proto"))
	if err != nil {
		t.Fatal(err)
	}

	if err := validateRemovedProtoFields(string(baseline), string(valid)); err != nil {
		t.Fatalf("reserved number/name fixture rejected: %v", err)
	}
	if err := validateRemovedProtoFields(string(baseline), string(invalid)); err == nil {
		t.Fatal("removed field without both reserved number and name unexpectedly passed")
	}
}

func TestProtoMutationFixturesAllowAdditionsAndRejectWireChanges(t *testing.T) {
	root := filepath.Join("testdata", "proto")
	baseline, err := os.ReadFile(filepath.Join(root, "mutation-baseline.proto"))
	if err != nil {
		t.Fatal(err)
	}
	additive, err := os.ReadFile(filepath.Join(root, "mutation-additive.proto"))
	if err != nil {
		t.Fatal(err)
	}
	renumbered, err := os.ReadFile(filepath.Join(root, "mutation-renumbered.proto"))
	if err != nil {
		t.Fatal(err)
	}
	typeChanged, err := os.ReadFile(filepath.Join(root, "mutation-type-changed.proto"))
	if err != nil {
		t.Fatal(err)
	}

	if err := validateProtoMutation(string(baseline), string(additive)); err != nil {
		t.Fatalf("additive proto mutation rejected: %v", err)
	}
	for name, candidate := range map[string][]byte{
		"renumbered":   renumbered,
		"type-changed": typeChanged,
	} {
		if err := validateProtoMutation(string(baseline), string(candidate)); err == nil {
			t.Errorf("forbidden %s proto mutation unexpectedly passed", name)
		}
	}
}

func validateProtoMutation(baseline, candidate string) error {
	baseFields := parseProtoFieldsWithType(baseline)
	nextFields := parseProtoFieldsWithType(candidate)
	for name, field := range baseFields {
		next, ok := nextFields[name]
		if !ok {
			return &wireMutationError{field: name, reason: "field removed"}
		}
		if field.number != next.number {
			return &wireMutationError{field: name, reason: "field number changed"}
		}
		if field.typ != next.typ {
			return &wireMutationError{field: name, reason: "field type changed"}
		}
	}
	return nil
}

func validateRemovedProtoFields(baseline, candidate string) error {
	baselineFields := parseProtoFields(baseline)
	candidateFields := parseProtoFields(candidate)
	reservedNumbers := map[int]bool{}
	reservedNames := map[string]bool{}
	reserved := regexp.MustCompile(`(?m)^\s*reserved\s+(.+);\s*$`)
	for _, match := range reserved.FindAllStringSubmatch(candidate, -1) {
		for _, item := range regexp.MustCompile(`"([^"]+)"|\b(\d+)\b`).FindAllStringSubmatch(match[1], -1) {
			if item[1] != "" {
				reservedNames[item[1]] = true
				continue
			}
			number, err := strconv.Atoi(item[2])
			if err == nil {
				reservedNumbers[number] = true
			}
		}
	}
	for key, field := range baselineFields {
		if _, stillPresent := candidateFields[key]; stillPresent {
			continue
		}
		if !reservedNumbers[field.number] || !reservedNames[field.name] {
			return &missingReservationError{name: field.name, number: field.number}
		}
	}
	return nil
}

func parseProtoFields(text string) map[string]protoField {
	fields := make(map[string]protoField)
	fieldPattern := regexp.MustCompile(`(?m)^\s*(?:optional\s+|repeated\s+)?[A-Za-z_][A-Za-z0-9_.<>]*\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(\d+)\s*;`)
	for _, match := range fieldPattern.FindAllStringSubmatch(text, -1) {
		number, err := strconv.Atoi(match[2])
		if err == nil {
			fields[match[1]] = protoField{name: match[1], number: number}
		}
	}
	return fields
}

func parseProtoFieldsWithType(text string) map[string]protoField {
	fields := make(map[string]protoField)
	fieldPattern := regexp.MustCompile(`(?m)^\s*(?:optional\s+|repeated\s+)?([A-Za-z_][A-Za-z0-9_.<>]*)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(\d+)\s*;`)
	for _, match := range fieldPattern.FindAllStringSubmatch(text, -1) {
		number, err := strconv.Atoi(match[3])
		if err == nil {
			fields[match[2]] = protoField{name: match[2], number: number, typ: match[1]}
		}
	}
	return fields
}

type missingReservationError struct {
	name   string
	number int
}

func (e *missingReservationError) Error() string {
	return "removed proto field requires reserved number and name: " + e.name
}

type wireMutationError struct {
	field  string
	reason string
}

func (e *wireMutationError) Error() string {
	return "forbidden proto mutation for " + e.field + ": " + e.reason
}
