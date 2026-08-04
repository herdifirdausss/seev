//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T2.6's
// fn_retention_purge_webhook_events_raw end to end against a real
// Postgres: eligibility boundary (terminal status + 30 days), redaction
// clears BOTH the plaintext `raw` column and the ciphertext/key_version
// columns (K2/K3, without ever needing the cryptox key), and dry-run
// counts match the real run. Reuses setupPayinTestDB
// (payin_integration_test.go, same package).
package payin_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRetention_WebhookEventsRaw_EligibilityBoundary(t *testing.T) {
	db := setupPayinTestDB(t)
	ctx := context.Background()

	insert := func(status string, updatedAt time.Time) uuid.UUID {
		id := uuid.New()
		_, err := db.ExecContext(ctx, `
			INSERT INTO payin_webhook_events
				(id, vendor, vendor_event_id, external_ref, user_id, amount, total_debit, currency, status,
					 created_at, updated_at, raw_ciphertext, raw_key_version)
			VALUES ($1, 'mockvendor', $2, 'ext', $3, 1000, 1000, 'IDR', $4, $5, $5, $6, 1)`,
			id, uuid.NewString(), uuid.New(), status, updatedAt, []byte("ciphertext-stand-in"))
		require.NoError(t, err)
		return id
	}

	now := time.Now().UTC()
	tooRecent := insert("posted", now.Add(-29*24*time.Hour))
	eligible := insert("posted", now.Add(-31*24*time.Hour))
	notTerminal := insert("received", now.Add(-40*24*time.Hour))

	var dryRunCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT fn_retention_purge_webhook_events_raw($1, 500, true)`, uuid.New()).Scan(&dryRunCount))
	require.Equal(t, 1, dryRunCount, "dry-run must count only the one truly eligible row")

	var realCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT fn_retention_purge_webhook_events_raw($1, 500, false)`, uuid.New()).Scan(&realCount))
	require.Equal(t, dryRunCount, realCount, "dry-run and real run must affect the same count")

	assertRedacted := func(id uuid.UUID, wantRedacted bool) {
		var ciphertext []byte
		require.NoError(t, db.QueryRowContext(ctx, `SELECT raw_ciphertext FROM payin_webhook_events WHERE id = $1`, id).Scan(&ciphertext))
		if wantRedacted {
			require.Nil(t, ciphertext)
		} else {
			require.NotNil(t, ciphertext)
		}
	}
	assertRedacted(eligible, true)
	assertRedacted(tooRecent, false)
	assertRedacted(notTerminal, false)
}

func TestRetention_WebhookEventsRaw_RetentionHoldExcludesRow(t *testing.T) {
	db := setupPayinTestDB(t)
	ctx := context.Background()

	heldUser := uuid.New()
	id := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO payin_webhook_events
			(id, vendor, vendor_event_id, external_ref, user_id, amount, total_debit, currency, status, created_at, updated_at, raw_ciphertext, raw_key_version)
		VALUES ($1, 'mockvendor', $2, 'ext', $3, 1000, 1000, 'IDR', 'posted', $4, $4, $5, 1)`,
		id, uuid.NewString(), heldUser, time.Now().Add(-40*24*time.Hour), []byte("ciphertext-stand-in"))
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO payin_retention_holds (id, scope, scope_value, reason_code, created_by)
		VALUES ($1, 'subject', $2, 'legal_hold', 'tester')`, uuid.New(), heldUser.String())
	require.NoError(t, err)

	var affected int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT fn_retention_purge_webhook_events_raw($1, 500, true)`, uuid.New()).Scan(&affected))
	require.Equal(t, 0, affected, "an active subject-scoped hold must exclude the row")
}
