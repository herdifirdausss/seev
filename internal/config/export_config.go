package config

import (
	"encoding/hex"
	"fmt"

	"github.com/herdifirdausss/seev/pkg/cryptox"
)

// ExportConfig is docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T4's (K9) dedicated,
// versioned KEK for user-export ZIP archives — same shape as CryptoxConfig
// (current version + version->hex-key map), but its own key namespace.
// Owned by auth-service only (the export coordinator); unlike
// LedgerIdempotency this is NOT required unconditionally — a
// missing/unconfigured ring simply means export creation is unavailable
// (ErrExportStorageUnavailable), the same "optional outside production,
// fails at first use, not at boot" convention every T2 field-encryption
// ring already uses. Export archives are a privacy convenience feature,
// not a money-safety invariant like T3's digest ring.
type ExportConfig struct {
	CurrentVersion int
	Keys           map[int]string // version -> hex-encoded 32-byte key
}

// Ring decodes Keys and constructs a *cryptox.Ring.
func (c ExportConfig) Ring() (*cryptox.Ring, error) {
	keys := make(map[int][]byte, len(c.Keys))
	for version, hexKey := range c.Keys {
		raw, err := hex.DecodeString(hexKey)
		if err != nil {
			return nil, fmt.Errorf("config: EXPORT_KEK_V%d is not valid hex: %w", version, err)
		}
		keys[version] = raw
	}
	return cryptox.NewRing(keys, c.CurrentVersion)
}

func loadExportConfig(getenv func(string) string) ExportConfig {
	return ExportConfig{
		CurrentVersion: parseInt(getenv("EXPORT_KEK_CURRENT_VERSION"), 1),
		Keys:           loadVersionedKeys(getenv, "EXPORT_KEK_V"),
	}
}
