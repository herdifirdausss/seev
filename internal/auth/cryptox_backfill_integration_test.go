//go:build integration

// Proves docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.5's bounded backfill for
// auth_users.email/full_name/email_lookup_digest and
// kyc_submissions.payload/rescreen_name/rescreen_birth_date: pre-migration
// (plaintext-only) rows get encrypted in bounded batches, and the batch
// loop is restartable — even when many rows share an identical created_at
// (the tie-break case a naive keyset cursor could get wrong) — because
// completion is defined by the WHERE-ciphertext-IS-NULL filter itself, not
// an external cursor. Reuses setupAuthTestDB, testRing, testLookupKey
// (cryptox_repository_integration_test.go, same package).
package auth_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/auth/repository"
)

func TestUserRepository_BackfillOnce_RestartableEqualTimestamps(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()

	const rowCount = 25
	sharedCreatedAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]uuid.UUID, rowCount)
	for i := 0; i < rowCount; i++ {
		ids[i] = uuid.New()
		_, err := db.ExecContext(ctx, `
			INSERT INTO auth_users (id, email, full_name, role, status, created_at, updated_at)
			VALUES ($1, $2, $3, 'user', 'active', $4, $4)`,
			ids[i], fmt.Sprintf("legacy-%d@example.test", i), fmt.Sprintf("Legacy %d", i), sharedCreatedAt)
		require.NoError(t, err)
	}

	repo := repository.NewUserRepository(db, testRing(t), testLookupKey(t))

	// Small batches force multiple BackfillOnce calls to land inside the
	// SAME created_at bucket — this is what would expose a naive
	// keyset-cursor tie-break bug; the IS-NULL-driven filter here has none
	// to get wrong.
	total := 0
	for i := 0; i < rowCount+5; i++ { // deliberately more iterations than needed — proves calling past completion is a safe no-op
		n, err := repo.BackfillOnce(ctx, 4)
		require.NoError(t, err)
		total += n
		if n == 0 {
			break
		}
	}
	require.Equal(t, rowCount, total, "every pre-migration row must be backfilled exactly once")

	// Plaintext absence scan (T2.5's own required check): a table-level
	// count, not just the per-row assertions below or BackfillOnce's own
	// return value — proves no row anywhere in auth_users was silently
	// skipped.
	var remaining int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM auth_users WHERE email_ciphertext IS NULL OR full_name_ciphertext IS NULL`).Scan(&remaining))
	require.Zero(t, remaining, "no auth_users row may still be missing ciphertext after backfill completes")

	for i, id := range ids {
		var ciphertext []byte
		require.NoError(t, db.QueryRowContext(ctx, `SELECT email_ciphertext FROM auth_users WHERE id = $1`, id).Scan(&ciphertext))
		require.NotEmpty(t, ciphertext, "row %d must be backfilled", i)

		got, err := repo.GetUserByEmail(ctx, fmt.Sprintf("legacy-%d@example.test", i))
		require.NoError(t, err)
		require.Equal(t, id, got.ID)
	}

	// Calling again after full completion must be a true no-op.
	n, err := repo.BackfillOnce(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

func TestKYCRepository_BackfillOnce_RestartableEqualTimestamps(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()

	const rowCount = 12
	sharedCreatedAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]uuid.UUID, rowCount)
	for i := 0; i < rowCount; i++ {
		userID := uuid.New()
		insertTestUser(t, db, userID)
		ids[i] = uuid.New()
		payload := fmt.Sprintf(`{"name":"Legacy Subject %d","birth_date":"1990-01-01"}`, i)
		_, err := db.ExecContext(ctx, `
			INSERT INTO kyc_submissions (id, user_id, level_requested, status, payload, provider, created_at)
			VALUES ($1, $2, 1, 'pending', $3::jsonb, 'test', $4)`,
			ids[i], userID, payload, sharedCreatedAt)
		require.NoError(t, err)
	}

	repo := repository.NewKYCRepository(db, testRing(t))

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
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM kyc_submissions WHERE payload_ciphertext IS NULL`).Scan(&remaining))
	require.Zero(t, remaining, "no kyc_submissions row may still be missing ciphertext after backfill completes")

	for i, id := range ids {
		got, err := repo.GetKYCSubmission(ctx, id)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("Legacy Subject %d", i), got.Payload["name"])

		var rescreenName string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT rescreen_name FROM kyc_submissions WHERE id = $1`, id).Scan(&rescreenName))
		require.Equal(t, fmt.Sprintf("Legacy Subject %d", i), rescreenName, "rescreen projection must be re-derived during backfill too")
	}
}
