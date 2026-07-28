package contracts_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestProtoPolicyUsesMajorPackagesAndSafeEnums(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "proto", "seev", "*", "v1", "*.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 6 {
		t.Fatalf("expected six v1 proto sources, got %d", len(files))
	}
	packagePattern := regexp.MustCompile(`(?m)^package\s+seev\.[a-z]+\.v1;`)
	enumPattern := regexp.MustCompile(`(?ms)^enum\s+\w+\s*\{(.*?)^\}`)
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !packagePattern.MatchString(text) {
			t.Errorf("%s is not a v1 major package", file)
		}
		for _, enum := range enumPattern.FindAllStringSubmatch(text, -1) {
			if !strings.Contains(enum[1], "= 0;") || !strings.Contains(enum[1], "UNSPECIFIED") {
				t.Errorf("%s enum has no explicit safe zero value: %s", file, enum[0])
			}
		}
	}
}

func TestProtoMutationFixturesStateWirePolicy(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "proto", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, marker := range []string{"add field", "reserved number and name", "renumber", "enum zero", "merge base"} {
		if !strings.Contains(strings.ToLower(text), marker) {
			t.Errorf("proto fixture policy omits %q", marker)
		}
	}
}
