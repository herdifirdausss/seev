package contract

import (
	"path/filepath"
	"testing"
)

func TestRepositoryAnalyticsContractsValidate(t *testing.T) {
	// Validate expects the analytics root because contracts/ is owned by the
	// analytics repository boundary, not the Go module root.
	root := filepath.Join("..", "..", "..")
	if errors := Validate(root); len(errors) != 0 {
		t.Fatalf("analytics contracts are invalid: %v", errors)
	}
}
