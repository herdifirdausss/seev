package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCalculateFiltersGeneratedMocksAndCommands(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/app\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "service", "cmd", "app"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "service"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "service", "logic.go"), []byte("package service\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "service", "repo_mock.go"), []byte("// Code generated. DO NOT EDIT.\n"), 0o600))
	profile := filepath.Join(root, "coverage.out")
	require.NoError(t, os.WriteFile(profile, []byte(`mode: atomic
example.test/app/service/logic.go:1.1,2.1 4 1
example.test/app/service/logic.go:3.1,4.1 6 0
example.test/app/service/repo_mock.go:1.1,2.1 100 1
example.test/app/service/cmd/app/main.go:1.1,2.1 100 1
`), 0o600))

	result, err := calculate(root, profile)
	require.NoError(t, err)
	require.Equal(t, 10, result.statements)
	require.Equal(t, 4, result.covered)
	require.Equal(t, 2, result.excludedBlocks)
	require.InDelta(t, 40.0, result.percentage(), 0.001)
}

func TestCalculateDeduplicatesBlocksAndKeepsCoveredExecution(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/app\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "service"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "service", "logic.go"), []byte("package service\n"), 0o600))
	profile := filepath.Join(root, "coverage.out")
	require.NoError(t, os.WriteFile(profile, []byte(`mode: atomic
example.test/app/service/logic.go:1.1,2.1 4 0
example.test/app/service/logic.go:1.1,2.1 4 1
example.test/app/service/logic.go:3.1,4.1 6 0
`), 0o600))

	result, err := calculate(root, profile)
	require.NoError(t, err)
	require.Equal(t, 10, result.statements)
	require.Equal(t, 4, result.covered)
	require.InDelta(t, 40.0, result.percentage(), 0.001)
}

func TestCalculateMatchingRiskGroup(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/app\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "money"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "messaging"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal", "money", "money.go"), []byte("package money\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal", "messaging", "broker.go"), []byte("package messaging\n"), 0o600))
	profile := filepath.Join(root, "coverage.out")
	require.NoError(t, os.WriteFile(profile, []byte(`mode: atomic
example.test/app/internal/money/money.go:1.1,2.1 8 1
example.test/app/internal/money/money.go:3.1,4.1 2 0
example.test/app/internal/messaging/broker.go:1.1,2.1 100 1
`), 0o600))

	result, err := calculateMatching(root, profile, []string{"internal/money/"})
	require.NoError(t, err)
	require.Equal(t, 10, result.statements)
	require.Equal(t, 8, result.covered)
	require.InDelta(t, 80, result.percentage(), 0.001)
}

func TestParseGroup(t *testing.T) {
	group, err := parseGroup("dr:60=operations/recovery/drverify/,operations/recovery/drreseed/")
	require.NoError(t, err)
	require.Equal(t, "dr", group.name)
	require.InDelta(t, 60, group.minimum, 0.001)
	require.Equal(t, []string{"operations/recovery/drverify/", "operations/recovery/drreseed/"}, group.prefixes)

	for _, raw := range []string{"", "money=path", "money:x=path", "money:101=path", "money:80="} {
		_, err := parseGroup(raw)
		require.Error(t, err, raw)
	}
}
