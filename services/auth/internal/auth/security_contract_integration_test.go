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

func TestSecurity_AppServiceCannotBypassKYCDecisionAuthorization(t *testing.T) {
	ownerDB, ownerCfg := setupAuthTestDBWithConfig(t)
	ctx := context.Background()
	m, _ := newAuthModuleWithMockKYC(ownerDB)

	user, _, err := m.Register(ctx, "kyc-boundary@example.com", "hunter22!", "KYC Boundary")
	require.NoError(t, err)
	_, err = m.SubmitKYC(ctx, user.ID, 1, nil)
	require.NoError(t, err)
	submission, err := m.SubmitKYC(ctx, user.ID, 2, map[string]any{"kyb_name": "Boundary Merchant"})
	require.NoError(t, err)
	require.Equal(t, "pending", submission.Status)

	const roleName = "test_security_kyc_app"
	const rolePassword = "security-kyc-contract-pw"
	_, err = ownerDB.ExecContext(ctx, `CREATE ROLE `+roleName+` LOGIN PASSWORD '`+rolePassword+`'`)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, `GRANT app_service TO `+roleName)
	require.NoError(t, err)
	appCfg := ownerCfg
	appCfg.User, appCfg.Password = roleName, rolePassword
	appDB, err := database.New(ctx, appCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })

	_, err = appDB.ExecContext(ctx, `
		INSERT INTO kyc_submissions
			(id, user_id, level_requested, status, provider,
			 payload_ciphertext, payload_key_version, rescreen_eligible)
		SELECT $1, user_id, level_requested, 'approved', provider,
		       payload_ciphertext, payload_key_version, rescreen_eligible
		FROM kyc_submissions WHERE id = $2`, uuid.New(), submission.ID)
	require.Error(t, err, "app_service must not insert a terminal submission without authorization")
	require.Contains(t, err.Error(), "KYC decision authorization is required")

	_, err = appDB.ExecContext(ctx, `
		UPDATE kyc_submissions
		SET status = 'approved', decided_by = 'raw-sql-checker', decided_at = now()
		WHERE id = $1`, submission.ID)
	require.Error(t, err, "app_service raw SQL must not bypass the authorized decision request")
	require.Contains(t, err.Error(), "KYC decision authorization is required")

	requestID := uuid.New()
	_, err = appDB.ExecContext(ctx, `
		INSERT INTO kyc_decision_requests
			(id, submission_id, requested_decision, request_reason, requested_by)
		VALUES ($1, $2, 'approve', 'self approval probe', 'same-actor')`, requestID, submission.ID)
	require.NoError(t, err)
	_, err = appDB.ExecContext(ctx, `
		UPDATE kyc_decision_requests
		SET status = 'authorized', decided_by = requested_by, decided_at = now()
		WHERE id = $1`, requestID)
	require.Error(t, err, "database CHECK must reject self-authorization even through raw SQL")

	_, err = appDB.ExecContext(ctx, `
		UPDATE kyc_decision_requests
		SET status = 'authorized', decided_by = 'different-checker', decided_at = now()
		WHERE id = $1`, requestID)
	require.NoError(t, err)
	_, err = appDB.ExecContext(ctx, `
		INSERT INTO kyc_decision_requests
			(id, submission_id, requested_decision, request_reason, requested_by)
		VALUES ($1, $2, 'reject', 'conflicting active decision', 'other-maker')`, uuid.New(), submission.ID)
	require.Error(t, err, "an authorized but unapplied decision must block conflicting requests")
}
