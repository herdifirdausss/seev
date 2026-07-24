package assurance

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/pkg/database"
)

// TestStartRetentionRunner_ConstructsAndStarts proves the wiring
// cmd/assurance-service/main.go depends on: NewRunner's two Class entries
// resolve to valid retentionworker.functionNamePattern names and the
// in-memory-lock scheduler starts without touching Postgres.
func TestStartRetentionRunner_ConstructsAndStarts(t *testing.T) {
	m := &Module{db: &database.MockDatabaseSQL{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	stop, err := m.StartRetentionRunner(logger)
	require.NoError(t, err)
	require.NotNil(t, stop)
	stop()
}
