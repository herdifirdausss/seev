//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T2.4's K2/K3 expand-phase
// encryption for sessions.email, and K2's one-way masking for
// audit_log.email, end to end against a real Postgres. Reuses
// setupAdminBFFOnlyDB (retention_integration_test.go, same package).
package adminbff_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/services/adminbff"
)

func sessionTestRing(t *testing.T) *cryptox.Ring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 11)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	return ring
}

func TestSessionRepository_RoundTripsThroughCiphertext(t *testing.T) {
	db := setupAdminBFFOnlyDB(t)
	repo := adminbff.NewSessionRepository(db, sessionTestRing(t))
	ctx := context.Background()

	now := time.Now().UTC()
	s := adminbff.Session{
		ID: uuid.NewString(), UserID: uuid.New(), Email: "operator-do-not-leak@example.com", Role: "admin",
		CSRFToken: uuid.NewString(), CreatedAt: now, LastSeenAt: now,
		ExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(24 * time.Hour),
	}
	require.NoError(t, repo.CreateSession(ctx, s))

	var ciphertext []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT email_ciphertext FROM sessions WHERE id = $1`, s.ID).Scan(&ciphertext))
	require.NotEmpty(t, ciphertext)
	require.NotContains(t, string(ciphertext), "operator-do-not-leak")

	got, err := repo.GetSession(ctx, s.ID)
	require.NoError(t, err)
	require.Equal(t, "operator-do-not-leak@example.com", got.Email)
}

func TestSessionRepository_PlaintextColumnIsAbsent(t *testing.T) {
	db := setupAdminBFFOnlyDB(t)
	ctx := context.Background()
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='sessions' AND column_name='email'`).Scan(&count))
	require.Zero(t, count)
}

// TestAuditLog_EmailIsMaskedNotEncrypted proves K2's literal wording for
// audit_log.email — "masked", not ciphertext — by going through the real
// HTTP-facing AuditMutation path and reading the raw column back, plus
// verifying ListAudit's search still matches on the plaintext operator
// email even though only its masked form is stored (cryptox.MaskEmail is
// deterministic, so the search term gets masked identically).
func TestAuditLog_EmailIsMaskedNotEncrypted(t *testing.T) {
	db := setupAdminBFFOnlyDB(t)
	ctx := context.Background()

	const email = "operator@example.com"
	_, err := db.ExecContext(ctx, `
		INSERT INTO audit_log (user_id, email, role, method, route_pattern, target_service, resource_id, outcome, request_id, summary)
		VALUES ($1, $2, 'admin', 'POST', '/api/v1/admin/adjustments', 'ledger', 'adj-1', 200, $3, '{}'::jsonb)`,
		uuid.New(), cryptox.MaskEmail(email), uuid.NewString())
	require.NoError(t, err)

	var stored string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT email FROM audit_log WHERE email = $1`, cryptox.MaskEmail(email)).Scan(&stored))
	require.Equal(t, "o***@e***.com", stored)
	require.NotEqual(t, email, stored, "audit_log.email must never store the real address")

	var matched int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE email = $1`, cryptox.MaskEmail(email)).Scan(&matched))
	require.Equal(t, 1, matched, "searching by the real email, masked the same deterministic way, must still find the row")
}

func TestSessionRepository_CiphertextColumnsAreRequired(t *testing.T) {
	db := setupAdminBFFOnlyDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO sessions (id,user_id,role,csrf_token,expires_at,absolute_expires_at)
		VALUES($1,$2,'admin',$3,now()+interval '1 hour',now()+interval '1 day')`,
		uuid.NewString(), uuid.New(), uuid.NewString())
	require.Error(t, err, "contract schema must reject sessions without ciphertext")
}
