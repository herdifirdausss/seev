//go:build integration

package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/database"
)

// TestSecurity_ClosureFinalizerUsesNarrowPrivilege proves the deployed role
// boundary rather than only inspecting migration text: app_service cannot
// DELETE credentials directly, but the closure worker's one purpose-built
// SECURITY DEFINER function can remove exactly the requested user's row.
func TestSecurity_ClosureFinalizerUsesNarrowPrivilege(t *testing.T) {
	ownerDB, ownerCfg := setupAuthTestDBWithConfig(t)
	ctx := context.Background()
	userID := uuid.New()
	insertTestUser(t, ownerDB, userID)
	_, err := ownerDB.ExecContext(ctx, `UPDATE auth_users SET status = 'closing' WHERE id = $1`, userID)
	require.NoError(t, err)

	const roleName = "test_security_closure_app"
	const rolePassword = "security-contract-pw"
	_, err = ownerDB.ExecContext(ctx, `CREATE ROLE `+roleName+` LOGIN PASSWORD '`+rolePassword+`'`)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, `GRANT app_service TO `+roleName)
	require.NoError(t, err)

	appCfg := ownerCfg
	appCfg.User, appCfg.Password = roleName, rolePassword
	appDB, err := database.New(ctx, appCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })

	_, err = appDB.ExecContext(ctx, `DELETE FROM auth_credentials WHERE user_id = $1`, userID)
	require.Error(t, err, "app_service must not have direct credential DELETE")

	var deleted int
	err = appDB.QueryRowContext(ctx,
		`SELECT public.fn_auth_finalize_credentials($1)`, userID).Scan(&deleted)
	require.NoError(t, err)
	require.Equal(t, 1, deleted)

	var remaining int
	require.NoError(t, ownerDB.QueryRowContext(ctx,
		`SELECT count(*) FROM auth_credentials WHERE user_id = $1`, userID).Scan(&remaining))
	require.Equal(t, 0, remaining)

	activeUserID := uuid.New()
	insertTestUser(t, ownerDB, activeUserID)
	var activeDeleted int
	require.NoError(t, appDB.QueryRowContext(ctx,
		`SELECT public.fn_auth_finalize_credentials($1)`, activeUserID).Scan(&activeDeleted))
	require.Equal(t, 0, activeDeleted, "the maintenance function must be bounded to closing identities")

	var securityDefiner bool
	require.NoError(t, ownerDB.QueryRowContext(ctx, `
		SELECT p.prosecdef
		FROM pg_proc p
		WHERE p.oid = 'public.fn_auth_finalize_credentials(uuid)'::regprocedure`).Scan(&securityDefiner))
	require.True(t, securityDefiner, "closure finalizer must remain SECURITY DEFINER")
}
