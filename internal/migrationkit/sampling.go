package migrationkit

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

const BasisPoints = 10_000

// CohortBucket returns a deterministic bucket in [0, 9999]. The migration
// name is part of the hash so separate migrations do not share cohorts.
func CohortBucket(stableKey, migrationName string) int {
	h := sha256.Sum256([]byte(stableKey + "\x00" + migrationName))
	return int(binary.BigEndian.Uint32(h[:4]) % BasisPoints)
}

func InCohort(stableKey, migrationName string, percentageBasisPoints int) bool {
	if percentageBasisPoints <= 0 {
		return false
	}
	if percentageBasisPoints >= BasisPoints {
		return true
	}
	return CohortBucket(stableKey, migrationName) < percentageBasisPoints
}

func StableKey(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	var key strings.Builder
	key.WriteString(parts[0])
	for _, part := range parts[1:] {
		key.WriteString("\x00" + part)
	}
	return key.String()
}
