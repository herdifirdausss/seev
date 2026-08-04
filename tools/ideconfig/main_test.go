package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/herdifirdausss/seev/tests/architecture"
)

func TestBuildLaunchFileUsesCanonicalServiceRegistry(t *testing.T) {
	launch := buildLaunchFile()
	if launch.Version != "0.2.0" {
		t.Fatalf("launch version = %q, want 0.2.0", launch.Version)
	}
	if len(launch.Configurations) != len(architecture.Services) {
		t.Fatalf("configurations = %d, want %d", len(launch.Configurations), len(architecture.Services))
	}

	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(filepath.Join(root, "..", ".."))
	seen := make(map[string]struct{}, len(launch.Configurations))
	for _, configuration := range launch.Configurations {
		if _, exists := seen[configuration.Name]; exists {
			t.Fatalf("duplicate launch configuration %q", configuration.Name)
		}
		seen[configuration.Name] = struct{}{}
		if configuration.Type != "go" || configuration.Request != "launch" || configuration.Mode != "debug" {
			t.Fatalf("unexpected debugger settings for %q: %#v", configuration.Name, configuration)
		}
		prefix := "${workspaceFolder}/"
		if len(configuration.Program) <= len(prefix) || configuration.Program[:len(prefix)] != prefix {
			t.Fatalf("program for %q does not use the workspace root: %q", configuration.Name, configuration.Program)
		}
		program := filepath.Join(root, filepath.FromSlash(configuration.Program[len(prefix):]))
		if _, err := os.Stat(program); err != nil {
			t.Fatalf("launch program for %q does not exist at %s: %v", configuration.Name, program, err)
		}
	}
}
