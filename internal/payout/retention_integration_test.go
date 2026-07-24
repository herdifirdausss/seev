//go:build integration

// Proves docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.6's
// fn_retention_purge_requests_destination_and_error end to end against a
// real Postgres: eligibility boundary (terminal status + 30 days),
// redaction clears the plaintext `destination`/`error_message` columns AND
// the destination_ciphertext/destination_key_version columns (without ever
// needing the cryptox key), and dry-run counts match the real run. Reuses
// setupPayoutTestDB (payout_integration_test.go, same package).
package payout_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRetention_RequestsDestinationAndError_EligibilityBoundary(t *testing.T) {
	db := setupPayoutTestDB(t)
	ctx := context.Background()

	insert := func(status string, updatedAt time.Time) uuid.UUID {
		id := uuid.New()
		_, err := db.ExecContext(ctx, `
			INSERT INTO payout_requests
				(id, user_id, amount, currency, vendor, destination, status, error_message, created_by,
				 created_at, updated_at, destination_ciphertext, destination_key_version)
			VALUES ($1, $2, 1000, 'IDR', 'mockvendor', '{"account_no":"x"}'::jsonb, $3, 'vendor timeout', 'test', $4, $4, $5, 1)`,
			id, uuid.New(), status, updatedAt, []byte("ciphertext-stand-in"))
		require.NoError(t, err)
		return id
	}

	now := time.Now().UTC()
	tooRecent := insert("settled", now.Add(-29*24*time.Hour))
	eligible := insert("settled", now.Add(-31*24*time.Hour))
	notTerminal := insert("submitted", now.Add(-40*24*time.Hour))

	var dryRunCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT fn_retention_purge_requests_destination_and_error($1, 500, true)`, uuid.New()).Scan(&dryRunCount))
	require.Equal(t, 1, dryRunCount, "dry-run must count only the one truly eligible row")

	var realCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT fn_retention_purge_requests_destination_and_error($1, 500, false)`, uuid.New()).Scan(&realCount))
	require.Equal(t, dryRunCount, realCount, "dry-run and real run must affect the same count")

	assertRedacted := func(id uuid.UUID, wantRedacted bool) {
		var destination []byte
		var ciphertext []byte
		var errorMessage *string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT destination, destination_ciphertext, error_message FROM payout_requests WHERE id = $1`, id).Scan(&destination, &ciphertext, &errorMessage))
		if wantRedacted {
			require.JSONEq(t, `{"redacted":true}`, string(destination))
			require.Nil(t, ciphertext)
			require.Nil(t, errorMessage)
		} else {
			require.JSONEq(t, `{"account_no":"x"}`, string(destination))
			require.NotNil(t, ciphertext)
			require.NotNil(t, errorMessage)
		}
	}
	assertRedacted(eligible, true)
	assertRedacted(tooRecent, false)
	assertRedacted(notTerminal, false)
}
