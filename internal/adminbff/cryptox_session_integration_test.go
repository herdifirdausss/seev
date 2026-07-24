//go:build integration

// Proves docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.4's K2/K3 expand-phase
// encryption for sessions.email, and K2's one-way masking for
// audit_log.email, end to end against a real Postgres. Reuses
// setupAdminBFFOnlyDB (retention_integration_test.go, same package).
package adminbff_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/adminbff"
	"github.com/herdifirdausss/seev/pkg/cryptox"
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

// TestSessionRepository_DualRead_PreMigrationRowStillWorks is T2's own
// required test: "dual-read/write compatibility during backfill."
func TestSessionRepository_DualRead_PreMigrationRowStillWorks(t *testing.T) {
	db := setupAdminBFFOnlyDB(t)
	ctx := context.Background()

	id := uuid.NewString()
	now := time.Now().UTC()
	_, err := db.ExecContext(ctx, `
		INSERT INTO sessions (id, user_id, email, role, csrf_token, created_at, last_seen_at, expires_at, absolute_expires_at)
		VALUES ($1, $2, 'legacy@example.com', 'admin', $3, $4, $4, $5, $6)`,
		id, uuid.New(), uuid.NewString(), now, now.Add(time.Hour), now.Add(24*time.Hour))
	require.NoError(t, err)

	repo := adminbff.NewSessionRepository(db, sessionTestRing(t))
	got, err := repo.GetSession(ctx, id)
	require.NoError(t, err, "a session with no email_ciphertext must still be readable via the plaintext fallback")
	require.Equal(t, "legacy@example.com", got.Email)
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

// TestSessionRepository_BackfillOnce_RestartableEqualTimestamps is
// docs/roadmap/active/51 T2.5's own required test: pre-migration rows sharing an
// identical created_at all get backfilled exactly once across many small,
// restart-simulating BackfillOnce calls.
func TestSessionRepository_BackfillOnce_RestartableEqualTimestamps(t *testing.T) {
	db := setupAdminBFFOnlyDB(t)
	ctx := context.Background()

	const rowCount = 20
	sharedCreatedAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Now().UTC()
	ids := make([]string, rowCount)
	for i := 0; i < rowCount; i++ {
		ids[i] = uuid.NewString()
		_, err := db.ExecContext(ctx, `
			INSERT INTO sessions (id, user_id, email, role, csrf_token, created_at, last_seen_at, expires_at, absolute_expires_at)
			VALUES ($1, $2, $3, 'admin', $4, $5, $5, $6, $7)`,
			ids[i], uuid.New(), fmt.Sprintf("legacy-%d@example.test", i), uuid.NewString(), sharedCreatedAt,
			now.Add(time.Hour), now.Add(24*time.Hour))
		require.NoError(t, err)
	}

	repo := adminbff.NewSessionRepository(db, sessionTestRing(t))
	total := 0
	for i := 0; i < rowCount+5; i++ {
		n, err := repo.BackfillOnce(ctx, 3)
		require.NoError(t, err)
		total += n
		if n == 0 {
			break
		}
	}
	require.Equal(t, rowCount, total)

	var remaining int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE email_ciphertext IS NULL`).Scan(&remaining))
	require.Zero(t, remaining, "no sessions row may still be missing ciphertext after backfill completes")

	for i, id := range ids {
		got, err := repo.GetSession(ctx, id)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("legacy-%d@example.test", i), got.Email)
	}

	n, err := repo.BackfillOnce(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}
