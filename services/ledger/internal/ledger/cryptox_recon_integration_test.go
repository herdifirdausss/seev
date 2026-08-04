//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T2.4's K2/K3 expand-phase
// encryption for recon_batches.source_filename and recon_items.raw end to
// end against a real Postgres: ciphertext round-trip and dual-read
// compatibility with pre-migration (plaintext-only) rows. Reuses
// setupLedgerOnlyDB (retention_integration_test.go, same package).
package ledger_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
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

// TestReconRepository_ContractRedactionHasNoPlaintextFallback proves the
// post-backfill contract: the plaintext columns are gone, and a retention
// redaction is represented only by cleared ciphertext.
func TestReconRepository_ContractRedactionHasNoPlaintextFallback(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()

	batchID := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO recon_batches (id, gateway, report_date, row_count, status, created_by, created_at)
		VALUES ($1, 'mockgateway', $2, 1, 'completed', 'test', now())`,
		batchID, reconTestReportDate)
	require.NoError(t, err)

	itemID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO recon_items (id, batch_id, external_ref, amount, match_status, created_at)
		VALUES ($1, $2, 'ext-redacted', 500, 'missing_internal', now())`,
		itemID, batchID)
	require.NoError(t, err)

	repo := repository.NewReconRepository(db, reconTestRing(t))

	gotBatch, err := repo.GetBatch(ctx, batchID)
	require.NoError(t, err)
	require.Equal(t, "REDACTED", gotBatch.SourceFilename)

	gotItem, err := repo.GetItem(ctx, itemID)
	require.NoError(t, err)
	require.Empty(t, gotItem.Raw)
}

func TestReconRepository_ContractDropsPlaintextColumns(t *testing.T) {
	db := setupLedgerOnlyDB(t)
	ctx := context.Background()
	for _, column := range []struct{ table, column string }{
		{"recon_batches", "source_filename"},
		{"recon_items", "raw"},
	} {
		var exists bool
		err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
			)`, column.table, column.column).Scan(&exists)
		require.NoError(t, err)
		require.Falsef(t, exists, "%s.%s must not retain plaintext", column.table, column.column)
	}
}
