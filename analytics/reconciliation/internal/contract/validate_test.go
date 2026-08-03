package contract

import (
	"path/filepath"
	"testing"
)

func TestRepositoryAnalyticsContractsValidate(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	if errors := Validate(root); len(errors) != 0 {
		t.Fatalf("analytics contracts are invalid: %v", errors)
	}
}
