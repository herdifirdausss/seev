//go:build integration

// Proves docs/roadmap/active/51-a8-data-lifecycle-privacy.md T5's own work
// item 2 (K10, A8 T5b): admin/operator accounts cannot use self-service
// closure (already proven by TestClosure_RequestClosure_AdminRejected) but
// CAN be closed through this separate maker/checker-approved path — and
// once approved, the SAME closure saga machinery T4b/T5b already proved
// live for self-service closure drives it to completion, across every
// registered owner.
package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/auth"
)

func TestOperatorOffboarding_HappyPath_ClosesTargetAcrossAllOwners(t *testing.T) {
	m, db, _ := setupMultiOwnerModule(t)
	ctx := context.Background()

	makerID := registerTestUser(t, m, "offboard-maker@example.test", "hunter22!")
	checkerID := registerTestUser(t, m, "offboard-checker@example.test", "hunter22!")
	targetID := registerTestUser(t, m, "offboard-target@example.test", "hunter22!")
	_, err := db.ExecContext(ctx, `UPDATE auth_users SET role = 'admin' WHERE id = $1`, targetID)
	require.NoError(t, err)

	proposal, err := m.ProposeOperatorOffboarding(ctx, makerID.String(), targetID, "role no longer needed")
	require.NoError(t, err)
	require.Equal(t, "pending", proposal.Status)

	decided, err := m.ApproveOperatorOffboarding(ctx, proposal.ID, checkerID.String())
	require.NoError(t, err)
	require.Equal(t, "approved", decided.Status)
	require.NotEqual(t, uuid.Nil, decided.ClosureRequestID)

	var targetStatus string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM auth_users WHERE id = $1`, targetID).Scan(&targetStatus))
	require.Equal(t, "closing", targetStatus, "approval must immediately disable the target, same as self-service RequestClosure")

	status := driveClosureToCompletion(t, m, db, decided.ClosureRequestID, 20)
	require.Equal(t, "completed", status)

	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM auth_users WHERE id = $1`, targetID).Scan(&targetStatus))
	require.Equal(t, "closed", targetStatus)
}

func TestOperatorOffboarding_SelfApproval_Rejected(t *testing.T) {
	m, db, _ := setupMultiOwnerModule(t)
	ctx := context.Background()

	makerID := registerTestUser(t, m, "offboard-self-maker@example.test", "hunter22!")
	targetID := registerTestUser(t, m, "offboard-self-target@example.test", "hunter22!")
	_, err := db.ExecContext(ctx, `UPDATE auth_users SET role = 'admin' WHERE id = $1`, targetID)
	require.NoError(t, err)

	proposal, err := m.ProposeOperatorOffboarding(ctx, makerID.String(), targetID, "testing self-approval")
	require.NoError(t, err)

	_, err = m.ApproveOperatorOffboarding(ctx, proposal.ID, makerID.String())
	require.ErrorIs(t, err, auth.ErrOperatorOffboardingSelfApproval)

	_, err = m.RejectOperatorOffboarding(ctx, proposal.ID, makerID.String())
	require.ErrorIs(t, err, auth.ErrOperatorOffboardingSelfApproval)
}

func TestOperatorOffboarding_TargetMustBeOperator(t *testing.T) {
	m, _, _ := setupMultiOwnerModule(t)
	ctx := context.Background()

	makerID := registerTestUser(t, m, "offboard-notop-maker@example.test", "hunter22!")
	targetID := registerTestUser(t, m, "offboard-notop-target@example.test", "hunter22!")

	_, err := m.ProposeOperatorOffboarding(ctx, makerID.String(), targetID, "wrong target")
	require.ErrorIs(t, err, auth.ErrOperatorOffboardingNotOperator)
}

func TestOperatorOffboarding_AlreadyDecided_SecondApprovalRejected(t *testing.T) {
	m, db, _ := setupMultiOwnerModule(t)
	ctx := context.Background()

	makerID := registerTestUser(t, m, "offboard-dup-maker@example.test", "hunter22!")
	checkerID := registerTestUser(t, m, "offboard-dup-checker@example.test", "hunter22!")
	otherCheckerID := registerTestUser(t, m, "offboard-dup-checker2@example.test", "hunter22!")
	targetID := registerTestUser(t, m, "offboard-dup-target@example.test", "hunter22!")
	_, err := db.ExecContext(ctx, `UPDATE auth_users SET role = 'admin' WHERE id = $1`, targetID)
	require.NoError(t, err)

	proposal, err := m.ProposeOperatorOffboarding(ctx, makerID.String(), targetID, "duplicate decision test")
	require.NoError(t, err)

	_, err = m.ApproveOperatorOffboarding(ctx, proposal.ID, checkerID.String())
	require.NoError(t, err)

	_, err = m.ApproveOperatorOffboarding(ctx, proposal.ID, otherCheckerID.String())
	require.ErrorIs(t, err, auth.ErrOperatorOffboardingAlreadyDecided)
}

func TestOperatorOffboarding_Reject_NeverStartsClosure(t *testing.T) {
	m, db, _ := setupMultiOwnerModule(t)
	ctx := context.Background()

	makerID := registerTestUser(t, m, "offboard-rej-maker@example.test", "hunter22!")
	checkerID := registerTestUser(t, m, "offboard-rej-checker@example.test", "hunter22!")
	targetID := registerTestUser(t, m, "offboard-rej-target@example.test", "hunter22!")
	_, err := db.ExecContext(ctx, `UPDATE auth_users SET role = 'admin' WHERE id = $1`, targetID)
	require.NoError(t, err)

	proposal, err := m.ProposeOperatorOffboarding(ctx, makerID.String(), targetID, "should be rejected")
	require.NoError(t, err)

	decided, err := m.RejectOperatorOffboarding(ctx, proposal.ID, checkerID.String())
	require.NoError(t, err)
	require.Equal(t, "rejected", decided.Status)

	var targetStatus string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM auth_users WHERE id = $1`, targetID).Scan(&targetStatus))
	require.Equal(t, "active", targetStatus, "a rejected proposal must never touch the target's status")

	var closureCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM privacy_requests WHERE user_id = $1`, targetID).Scan(&closureCount))
	require.Equal(t, 0, closureCount)
}
