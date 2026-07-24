package config

import (
	"encoding/hex"
	"fmt"

	"github.com/herdifirdausss/seev/pkg/cryptox"
)

// ClosureConfig is docs/roadmap/active/51-a8-data-lifecycle-privacy.md T5's (K10) dedicated,
// versioned KEK for the account-closure saga's active-subject ciphertext —
// same shape as ExportConfig, its own key namespace. Owned by auth-service
// only. Optional like ExportConfig (a privacy-saga convenience key, not a
// money-safety invariant like T3's digest ring): a missing/unconfigured
// ring simply means closure requests can't be created yet.
type ClosureConfig struct {
	CurrentVersion int
	Keys           map[int]string // version -> hex-encoded 32-byte key
}

// Ring decodes Keys and constructs a *cryptox.Ring.
func (c ClosureConfig) Ring() (*cryptox.Ring, error) {
	keys := make(map[int][]byte, len(c.Keys))
	for version, hexKey := range c.Keys {
		raw, err := hex.DecodeString(hexKey)
		if err != nil {
			return nil, fmt.Errorf("config: CLOSURE_KEK_V%d is not valid hex: %w", version, err)
		}
		keys[version] = raw
	}
	return cryptox.NewRing(keys, c.CurrentVersion)
}

func loadClosureConfig(getenv func(string) string) ClosureConfig {
	return ClosureConfig{
		CurrentVersion: parseInt(getenv("CLOSURE_KEK_CURRENT_VERSION"), 1),
		Keys:           loadVersionedKeys(getenv, "CLOSURE_KEK_V"),
	}
}
