package contracts_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestContractArtifactsContainSyntheticDataOnly(t *testing.T) {
	root := filepath.Join("..", "..")
	paths := []string{
		filepath.Join(root, "api", "contracts"),
		filepath.Join(root, "api", "events"),
		filepath.Join(root, "api", "openapi"),
		filepath.Join(root, "docs", "performance"),
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`\b(?:sk|pk)_(?:live|test)_[A-Za-z0-9]{12,}\b`),
		regexp.MustCompile(`\b(?:ghp|github_pat)_[A-Za-z0-9_]{20,}\b`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@example\.com\b`),
	}
	for _, base := range paths {
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !isContractText(path) {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(body)
			for _, pattern := range patterns {
				if match := pattern.FindString(text); match != "" {
					t.Errorf("%s contains a non-synthetic contract artifact value matching %q", path, match)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func isContractText(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml", ".json", ".md", ".js":
		return true
	default:
		return false
	}
}
