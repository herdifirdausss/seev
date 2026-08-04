package config

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadFromEnv_CryptoxKeyFile_WinsOverPlainEnv proves the Docker
// secrets mount path (CRYPTOX_KEY_V1_FILE) — docs/roadmap/archive/51 T2.2's
// compose-secrets requirement — takes precedence over a plain
// CRYPTOX_KEY_V1 env var, matching operations/agents/backup's own
// BACKUP_PASSWORD_FILE convention.
func TestLoadFromEnv_CryptoxKeyFile_WinsOverPlainEnv(t *testing.T) {
	fromFile := hex.EncodeToString([]byte("this-is-the-file-value-32-bytes"))
	fromEnv := hex.EncodeToString([]byte("this-is-the-plain-env-value-32b"))

	path := filepath.Join(t.TempDir(), "cryptox_key_v1")
	require.NoError(t, os.WriteFile(path, []byte(fromFile+"\n"), 0o600))

	cfg, err := loadFromEnv(validEnv(map[string]string{
		"CRYPTOX_KEY_V1_FILE": path,
		"CRYPTOX_KEY_V1":      fromEnv,
	}))
	require.NoError(t, err)
	assert.Equal(t, fromFile, cfg.Cryptox.Keys[1])
}

func TestLoadFromEnv_CryptoxKeyFile_MissingFileFallsBackToEnv(t *testing.T) {
	fromEnv := hex.EncodeToString([]byte("this-is-the-plain-env-value-32b"))
	cfg, err := loadFromEnv(validEnv(map[string]string{
		"CRYPTOX_KEY_V1_FILE": "/nonexistent/path",
		"CRYPTOX_KEY_V1":      fromEnv,
	}))
	require.NoError(t, err)
	assert.Equal(t, fromEnv, cfg.Cryptox.Keys[1])
}
