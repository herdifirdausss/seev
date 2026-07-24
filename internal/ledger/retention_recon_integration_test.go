//go:build integration

// Proves docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.6's
// fn_retention_purge_recon_batches and fn_retention_purge_recon_items end
// to end against a real Postgres: eligibility boundary (terminal status +
// 90 days), recon_items' eligibility being driven by its PARENT
// recon_batches row (not any column on recon_items itself), redaction
// clears both the plaintext and ciphertext/key_version columns, and
// dry-run counts match the real run. Reuses setupLedgerOnlyDB
// (retention_integration_test.go, same package).
package ledger_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRetention_ReconBatches_EligibilityBoundary(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()

	insert := func(status string, createdAt time.Time) uuid.UUID {
		id := uuid.New()
		_, err := db.ExecContext(ctx, `
			INSERT INTO recon_batches (id, gateway, report_date, source_filename, row_count, status, created_by, created_at,
				source_filename_ciphertext, source_filename_key_version)
			VALUES ($1, 'mockgateway', $2, 'settlement.csv', 1, $3, 'test', $4, $5, 1)`,
			id, createdAt, status, createdAt, []byte("ciphertext-stand-in"))
		require.NoError(t, err)
		return id
	}

	now := time.Now().UTC()
	tooRecent := insert("completed", now.Add(-89*24*time.Hour))
	eligible := insert("completed", now.Add(-91*24*time.Hour))
	notTerminal := insert("processing", now.Add(-120*24*time.Hour))

	var dryRunCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT fn_retention_purge_recon_batches($1, 500, true)`, uuid.New()).Scan(&dryRunCount))
	require.Equal(t, 1, dryRunCount)

	var realCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT fn_retention_purge_recon_batches($1, 500, false)`, uuid.New()).Scan(&realCount))
	require.Equal(t, dryRunCount, realCount)

	assertRedacted := func(id uuid.UUID, wantRedacted bool) {
		var sourceFilename string
		var ciphertext []byte
		require.NoError(t, db.QueryRowContext(ctx, `SELECT source_filename, source_filename_ciphertext FROM recon_batches WHERE id = $1`, id).Scan(&sourceFilename, &ciphertext))
		if wantRedacted {
			require.Equal(t, "REDACTED", sourceFilename)
			require.Nil(t, ciphertext)
		} else {
			require.Equal(t, "settlement.csv", sourceFilename)
			require.NotNil(t, ciphertext)
		}
	}
	assertRedacted(eligible, true)
	assertRedacted(tooRecent, false)
	assertRedacted(notTerminal, false)
}

// TestRetention_ReconBatches_RetentionHoldExcludesRow proves the
// resource-scoped hold path — recon_batches has no subject (user_id)
// column, so this is the only hold scope that can ever cover it (besides
// table/time_range), unlike fee_quotes' own subject-scoped hold test.
func TestRetention_ReconBatches_RetentionHoldExcludesRow(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()

	id := uuid.New()
	createdAt := time.Now().UTC().Add(-91 * 24 * time.Hour)
	_, err := db.ExecContext(ctx, `
		INSERT INTO recon_batches (id, gateway, report_date, source_filename, row_count, status, created_by, created_at, source_filename_ciphertext, source_filename_key_version)
		VALUES ($1, 'mockgateway', $2, 'settlement.csv', 1, 'completed', 'test', $3, $4, 1)`,
		id, createdAt, createdAt, []byte("ciphertext-stand-in"))
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_retention_holds (id, scope, scope_value, reason_code, created_by)
		VALUES ($1, 'resource', $2, 'legal_hold', 'tester')`, uuid.New(), id.String())
	require.NoError(t, err)

	var affected int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT fn_retention_purge_recon_batches($1, 500, true)`, uuid.New()).Scan(&affected))
	require.Equal(t, 0, affected, "an active resource-scoped hold must exclude the row")
}

func TestRetention_ReconItems_EligibilityFollowsParentBatch(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()

	now := time.Now().UTC()

	insertBatch := func(status string, createdAt time.Time) uuid.UUID {
		id := uuid.New()
		_, err := db.ExecContext(ctx, `
			INSERT INTO recon_batches (id, gateway, report_date, source_filename, row_count, status, created_by, created_at)
			VALUES ($1, 'mockgateway', $2, 'settlement.csv', 1, $3, 'test', $4)`,
			id, createdAt, status, createdAt)
		require.NoError(t, err)
		return id
	}
	insertItem := func(batchID uuid.UUID) uuid.UUID {
		id := uuid.New()
		_, err := db.ExecContext(ctx, `
			INSERT INTO recon_items (id, batch_id, external_ref, amount, raw, match_status, created_at, raw_ciphertext, raw_key_version)
			VALUES ($1, $2, 'ext-1', 500, '{"secret":"x"}'::jsonb, 'matched', now(), $3, 1)`,
			id, batchID, []byte("ciphertext-stand-in"))
		require.NoError(t, err)
		return id
	}

	eligibleBatch := insertBatch("completed", now.Add(-91*24*time.Hour))
	eligibleItem := insertItem(eligibleBatch)

	freshBatch := insertBatch("completed", now.Add(-10*24*time.Hour))
	freshItem := insertItem(freshBatch)

	openBatch := insertBatch("processing", now.Add(-120*24*time.Hour))
	openBatchItem := insertItem(openBatch)

	var dryRunCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT fn_retention_purge_recon_items($1, 500, true)`, uuid.New()).Scan(&dryRunCount))
	require.Equal(t, 1, dryRunCount, "only the item under a terminal-90d+ batch is eligible")

	var realCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT fn_retention_purge_recon_items($1, 500, false)`, uuid.New()).Scan(&realCount))
	require.Equal(t, dryRunCount, realCount)

	assertRedacted := func(id uuid.UUID, wantRedacted bool) {
		var raw []byte
		var ciphertext []byte
		require.NoError(t, db.QueryRowContext(ctx, `SELECT raw, raw_ciphertext FROM recon_items WHERE id = $1`, id).Scan(&raw, &ciphertext))
		if wantRedacted {
			require.Nil(t, raw)
			require.Nil(t, ciphertext)
		} else {
			require.JSONEq(t, `{"secret":"x"}`, string(raw))
			require.NotNil(t, ciphertext)
		}
	}
	assertRedacted(eligibleItem, true)
	assertRedacted(freshItem, false)
	assertRedacted(openBatchItem, false)
}

// TestRetention_TransactionsIdempotencyRaw_RequiresDigestFirst is
// docs/roadmap/active/51 T3's own required test for its own work item 5:
// "add retention redaction of raw key/scope after 30 days" — plus the
// load-bearing guard fn_retention_purge_transactions_idempotency_raw adds
// on top of the generic pattern: a terminal, 30+ day old row with NO
// digest yet must NEVER be redacted (that would silently and irreversibly
// disable deduplication for it), even though it otherwise matches every
// other eligibility condition.
func TestRetention_TransactionsIdempotencyRaw_RequiresDigestFirst(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()

	insert := func(status string, updatedAt time.Time, withDigest bool) uuid.UUID {
		id := uuid.New()
		var digest []byte
		if withDigest {
			digest = []byte("digest-stand-in-" + id.String())
		}
		_, err := db.ExecContext(ctx, `
			INSERT INTO ledger_transactions (id, idempotency_key, idempotency_scope, type, status, amount, currency, created_at, updated_at,
				idempotency_key_digest, idempotency_key_version, conflict_fingerprint)
			VALUES ($1, $2, NULL, 'money_in', $3, 1000, 'IDR', $4, $4, $5, 1, 'fingerprint-stand-in')`,
			id, uuid.NewString(), status, updatedAt, digest)
		require.NoError(t, err)
		return id
	}

	now := time.Now().UTC()
	eligible := insert("posted", now.Add(-31*24*time.Hour), true)
	tooRecent := insert("posted", now.Add(-29*24*time.Hour), true)
	notTerminal := insert("pending", now.Add(-40*24*time.Hour), true)
	noDigestYet := insert("posted", now.Add(-40*24*time.Hour), false)

	var dryRunCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT fn_retention_purge_transactions_idempotency_raw($1, 500, true)`, uuid.New()).Scan(&dryRunCount))
	require.Equal(t, 1, dryRunCount, "dry-run must count only the one truly eligible (terminal, aged, AND already-digested) row")

	var realCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT fn_retention_purge_transactions_idempotency_raw($1, 500, false)`, uuid.New()).Scan(&realCount))
	require.Equal(t, dryRunCount, realCount)

	assertRedacted := func(id uuid.UUID, wantRedacted bool) {
		var key *string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT idempotency_key FROM ledger_transactions WHERE id = $1`, id).Scan(&key))
		if wantRedacted {
			require.Nil(t, key)
		} else {
			require.NotNil(t, key)
		}
	}
	assertRedacted(eligible, true)
	assertRedacted(tooRecent, false)
	assertRedacted(notTerminal, false)
	assertRedacted(noDigestYet, false)
}
