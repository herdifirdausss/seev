package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProductionPostingDoesNotBypassCommandBoundary is intentionally a small
// source-boundary test. The low-level handle service is allowed to be used by
// the command package's composition root, but feature packages and transports
// must not call it directly. Tests may use the low-level service to exercise
// database contracts; production source files may not.
func TestProductionPostingDoesNotBypassCommandBoundary(t *testing.T) {
	root := "."
	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(contents)
		if !strings.HasPrefix(filepath.ToSlash(path), "command/") &&
			(strings.Contains(text, "handleSvc.Handle(") || strings.Contains(text, ".Handle(ctx")) {
			violations = append(violations, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("low-level ledger posting bypasses shared command executor: %s", strings.Join(violations, ", "))
	}
}
