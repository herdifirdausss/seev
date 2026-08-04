package notify

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/database"
)

// TestStartRetentionRunner_ConstructsAndStarts proves the wiring
// services/gateway/cmd/gateway/main.go depends on: NewRunner's two Class entries resolve to
// valid retentionworker.functionNamePattern names and the scheduler starts
// without touching Postgres (registering a cron job does not itself run a
// query — the same reasoning services/auth's own StartRetentionRunner test
// coverage relies on).
func TestStartRetentionRunner_ConstructsAndStarts(t *testing.T) {
	m, _ := newModule(t)
	m.db = &database.MockDatabaseSQL{}

	stop, err := m.StartRetentionRunner(nil, discardLogger())
	require.NoError(t, err)
	require.NotNil(t, stop)
	stop()
}
