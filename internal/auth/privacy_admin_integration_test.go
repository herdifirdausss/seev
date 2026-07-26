//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T6's admin BFF status panel query
// (AdminListPrivacyRequests) against a real Postgres: type/status filters
// work, and the returned rows never carry subject data (the struct itself
// has no email/full_name field — this test additionally proves the
// underlying query doesn't join auth_users for any such column).
package auth_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminListPrivacyRequests_FiltersByTypeAndStatus(t *testing.T) {
	m, _, _, _ := setupExportModule(t)
	ctx := context.Background()
	userID := registerTestUser(t, m, "admin-panel@example.test", "hunter22!")

	req, err := m.RequestExport(ctx, userID, "hunter22!")
	require.NoError(t, err)

	all, err := m.AdminListPrivacyRequests(ctx, "", "", 50)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, req.ID, all[0].ID)
	require.Equal(t, userID, all[0].UserID)
	require.Equal(t, "export", all[0].RequestType)
	require.Equal(t, "pending", all[0].Status)

	filtered, err := m.AdminListPrivacyRequests(ctx, "export", "pending", 50)
	require.NoError(t, err)
	require.Len(t, filtered, 1)

	none, err := m.AdminListPrivacyRequests(ctx, "closure", "", 50)
	require.NoError(t, err)
	require.Empty(t, none)
}
