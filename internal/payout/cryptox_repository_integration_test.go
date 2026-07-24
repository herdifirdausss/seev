//go:build integration

// Proves docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.4's K2/K3 expand-phase
// encryption for payout_requests.destination end to end against a real
// Postgres: ciphertext round-trip and dual-read compatibility with a
// pre-migration (plaintext-only) row. Reuses setupPayoutTestDB
// (payout_integration_test.go, same package).
package payout_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/payout/repository"
	"github.com/herdifirdausss/seev/pkg/cryptox"
)

func payoutTestRing(t *testing.T) *cryptox.Ring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 5)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	return ring
}

func TestPayoutRepository_Insert_RoundTripsThroughCiphertext(t *testing.T) {
	db := setupPayoutTestDB(t)
	repo := repository.NewRepository(db, payoutTestRing(t))
	ctx := context.Background()

	req := model.PayoutRequest{
		ID: uuid.New(), UserID: uuid.New(), Amount: decimal.NewFromInt(50000), Currency: "IDR",
		Vendor: "mockvendor", Destination: []byte(`{"account_no":"1234567890"}`), CreatedBy: "test",
	}
	require.NoError(t, repo.Insert(ctx, req))

	var ciphertext []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT destination_ciphertext FROM payout_requests WHERE id = $1`, req.ID).Scan(&ciphertext))
	require.NotEmpty(t, ciphertext)
	require.NotContains(t, string(ciphertext), "1234567890")

	got, err := repo.Get(ctx, req.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{"account_no":"1234567890"}`, string(got.Destination))
}

// TestPayoutRepository_DualRead_PreMigrationRowStillWorks is T2's own
// required test: "dual-read/write compatibility during backfill."
func TestPayoutRepository_DualRead_PreMigrationRowStillWorks(t *testing.T) {
	db := setupPayoutTestDB(t)
	ctx := context.Background()

	id := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO payout_requests (id, user_id, amount, currency, vendor, destination, status, created_by)
		VALUES ($1, $2, 1000, 'IDR', 'mockvendor', '{"legacy":true}'::jsonb, 'created', 'test')`,
		id, uuid.New())
	require.NoError(t, err)

	repo := repository.NewRepository(db, payoutTestRing(t))
	got, err := repo.Get(ctx, id)
	require.NoError(t, err, "a row with no destination_ciphertext must still be readable via the plaintext fallback")
	require.JSONEq(t, `{"legacy":true}`, string(got.Destination))
}

// TestPayoutRepository_BackfillOnce_RestartableEqualTimestamps is docs/roadmap/active/51
// T2.5's own required test: pre-migration rows sharing an identical
// created_at all get backfilled exactly once across many small,
// restart-simulating BackfillOnce calls.
func TestPayoutRepository_BackfillOnce_RestartableEqualTimestamps(t *testing.T) {
	db := setupPayoutTestDB(t)
	ctx := context.Background()

	const rowCount = 20
	sharedCreatedAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]uuid.UUID, rowCount)
	for i := 0; i < rowCount; i++ {
		ids[i] = uuid.New()
		dest := fmt.Sprintf(`{"account_no":"legacy-%d"}`, i)
		_, err := db.ExecContext(ctx, `
			INSERT INTO payout_requests (id, user_id, amount, currency, vendor, destination, status, created_by, created_at, updated_at)
			VALUES ($1, $2, 1000, 'IDR', 'mockvendor', $3::jsonb, 'created', 'test', $4, $4)`,
			ids[i], uuid.New(), dest, sharedCreatedAt)
		require.NoError(t, err)
	}

	repo := repository.NewRepository(db, payoutTestRing(t))
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
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM payout_requests WHERE destination_ciphertext IS NULL`).Scan(&remaining))
	require.Zero(t, remaining, "no payout_requests row may still be missing ciphertext after backfill completes")

	for i, id := range ids {
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		require.JSONEq(t, fmt.Sprintf(`{"account_no":"legacy-%d"}`, i), string(got.Destination))
	}

	n, err := repo.BackfillOnce(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}
