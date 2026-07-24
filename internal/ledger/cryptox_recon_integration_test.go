//go:build integration

// Proves docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.4's K2/K3 expand-phase
// encryption for recon_batches.source_filename and recon_items.raw end to
// end against a real Postgres: ciphertext round-trip and dual-read
// compatibility with pre-migration (plaintext-only) rows. Reuses
// setupLedgerOnlyDB (retention_integration_test.go, same package).
package ledger_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/cryptox"
)

var reconTestReportDate = time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)

func reconTestRing(t *testing.T) *cryptox.Ring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 7)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	return ring
}

func TestReconRepository_RoundTripsThroughCiphertext(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	repo := repository.NewReconRepository(db, reconTestRing(t))
	ctx := context.Background()

	batch := model.ReconBatch{
		ID: uuid.New(), Gateway: "mockgateway", ReportDate: reconTestReportDate,
		SourceFilename: "settlement-do-not-leak.csv", RowCount: 1, Status: "processing", CreatedBy: "test",
	}
	item := model.ReconItem{
		ID: uuid.New(), BatchID: batch.ID, ExternalRef: "ext-1",
		Amount: decimal.NewFromInt(1000), Raw: json.RawMessage(`{"secret":"do-not-leak"}`), MatchStatus: "missing_internal",
	}
	require.NoError(t, db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if err := repo.CreateBatch(ctx, tx, batch); err != nil {
			return err
		}
		return repo.InsertItems(ctx, tx, []model.ReconItem{item})
	}))

	var filenameCiphertext, rawCiphertext []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT source_filename_ciphertext FROM recon_batches WHERE id = $1`, batch.ID).Scan(&filenameCiphertext))
	require.NotEmpty(t, filenameCiphertext)
	require.NotContains(t, string(filenameCiphertext), "do-not-leak")

	require.NoError(t, db.QueryRowContext(ctx, `SELECT raw_ciphertext FROM recon_items WHERE id = $1`, item.ID).Scan(&rawCiphertext))
	require.NotEmpty(t, rawCiphertext)
	require.NotContains(t, string(rawCiphertext), "do-not-leak")

	gotBatch, err := repo.GetBatch(ctx, batch.ID)
	require.NoError(t, err)
	require.Equal(t, "settlement-do-not-leak.csv", gotBatch.SourceFilename)

	gotItem, err := repo.GetItem(ctx, item.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{"secret":"do-not-leak"}`, string(gotItem.Raw))
}

// TestReconRepository_DualRead_PreMigrationRowsStillWork is T2's own
// required test: "dual-read/write compatibility during backfill."
func TestReconRepository_DualRead_PreMigrationRowsStillWork(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()

	batchID := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO recon_batches (id, gateway, report_date, source_filename, row_count, status, created_by, created_at)
		VALUES ($1, 'mockgateway', $2, 'legacy.csv', 1, 'processing', 'test', now())`,
		batchID, reconTestReportDate)
	require.NoError(t, err)

	itemID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO recon_items (id, batch_id, external_ref, amount, raw, match_status, created_at)
		VALUES ($1, $2, 'ext-legacy', 500, '{"legacy":true}'::jsonb, 'missing_internal', now())`,
		itemID, batchID)
	require.NoError(t, err)

	repo := repository.NewReconRepository(db, reconTestRing(t))

	gotBatch, err := repo.GetBatch(ctx, batchID)
	require.NoError(t, err, "a batch with no source_filename_ciphertext must still be readable via the plaintext fallback")
	require.Equal(t, "legacy.csv", gotBatch.SourceFilename)

	gotItem, err := repo.GetItem(ctx, itemID)
	require.NoError(t, err, "an item with no raw_ciphertext must still be readable via the plaintext fallback")
	require.JSONEq(t, `{"legacy":true}`, string(gotItem.Raw))
}

// TestReconRepository_BackfillOnce_RestartableEqualTimestamps is docs/roadmap/active/51
// T2.5's own required test: pre-migration rows (across BOTH recon_batches
// and recon_items) sharing an identical created_at all get backfilled
// exactly once across many small, restart-simulating BackfillOnce calls.
func TestReconRepository_BackfillOnce_RestartableEqualTimestamps(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()

	const batchCount = 8
	sharedCreatedAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	batchIDs := make([]uuid.UUID, batchCount)
	itemIDs := make([]uuid.UUID, batchCount)
	for i := 0; i < batchCount; i++ {
		batchIDs[i] = uuid.New()
		_, err := db.ExecContext(ctx, `
			INSERT INTO recon_batches (id, gateway, report_date, source_filename, row_count, status, created_by, created_at)
			VALUES ($1, 'mockgateway', $2, $3, 1, 'processing', 'test', $4)`,
			batchIDs[i], reconTestReportDate, fmt.Sprintf("legacy-%d.csv", i), sharedCreatedAt)
		require.NoError(t, err)

		itemIDs[i] = uuid.New()
		_, err = db.ExecContext(ctx, `
			INSERT INTO recon_items (id, batch_id, external_ref, amount, raw, match_status, created_at)
			VALUES ($1, $2, $3, 500, $4::jsonb, 'missing_internal', $5)`,
			itemIDs[i], batchIDs[i], fmt.Sprintf("ext-legacy-%d", i), fmt.Sprintf(`{"secret":"legacy-%d"}`, i), sharedCreatedAt)
		require.NoError(t, err)
	}

	repo := repository.NewReconRepository(db, reconTestRing(t))
	total := 0
	for i := 0; i < 2*batchCount+5; i++ {
		n, err := repo.BackfillOnce(ctx, 3)
		require.NoError(t, err)
		total += n
		if n == 0 {
			break
		}
	}
	require.Equal(t, 2*batchCount, total, "both recon_batches and recon_items rows must be backfilled")

	var remainingBatches, remainingItems int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM recon_batches WHERE source_filename_ciphertext IS NULL`).Scan(&remainingBatches))
	require.Zero(t, remainingBatches, "no recon_batches row may still be missing ciphertext after backfill completes")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM recon_items WHERE raw IS NOT NULL AND raw_ciphertext IS NULL`).Scan(&remainingItems))
	require.Zero(t, remainingItems, "no recon_items row with a raw value may still be missing ciphertext after backfill completes")

	for i := range batchIDs {
		gotBatch, err := repo.GetBatch(ctx, batchIDs[i])
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("legacy-%d.csv", i), gotBatch.SourceFilename)

		gotItem, err := repo.GetItem(ctx, itemIDs[i])
		require.NoError(t, err)
		require.JSONEq(t, fmt.Sprintf(`{"secret":"legacy-%d"}`, i), string(gotItem.Raw))
	}

	n, err := repo.BackfillOnce(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}
