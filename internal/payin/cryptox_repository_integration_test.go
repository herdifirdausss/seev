//go:build integration

// Proves docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.4's K2/K3 expand-phase
// encryption for payin_webhook_events.raw end to end against a real
// Postgres: ciphertext round-trip and dual-read compatibility with a
// pre-migration (plaintext-only) row. Reuses setupPayinTestDB
// (payin_integration_test.go, same package).
package payin_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/internal/payin/repository"
	"github.com/herdifirdausss/seev/pkg/cryptox"
)

func payinTestRing(t *testing.T) *cryptox.Ring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	return ring
}

func TestPayinRepository_GetOrInsert_RoundTripsThroughCiphertext(t *testing.T) {
	db := setupPayinTestDB(t)
	repo := repository.NewRepository(db, payinTestRing(t))
	ctx := context.Background()

	ev := model.WebhookEvent{
		ID: uuid.New(), Vendor: "mockvendor", VendorEventID: uuid.NewString(),
		ExternalRef: "ext-1", UserID: uuid.New(), Amount: decimal.NewFromInt(10000), Currency: "IDR",
		Raw: []byte(`{"secret":"do-not-leak"}`),
	}
	_, err := repo.GetOrInsert(ctx, ev)
	require.NoError(t, err)

	var ciphertext []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT raw_ciphertext FROM payin_webhook_events WHERE id = $1`, ev.ID).Scan(&ciphertext))
	require.NotEmpty(t, ciphertext)
	require.NotContains(t, string(ciphertext), "do-not-leak")

	got, err := repo.Get(ctx, ev.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{"secret":"do-not-leak"}`, string(got.Raw))
}

// TestPayinRepository_DualRead_PreMigrationRowStillWorks is T2's own
// required test: "dual-read/write compatibility during backfill."
func TestPayinRepository_DualRead_PreMigrationRowStillWorks(t *testing.T) {
	db := setupPayinTestDB(t)
	ctx := context.Background()

	id := uuid.New()
	userID := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO payin_webhook_events (id, vendor, vendor_event_id, external_ref, user_id, amount, currency, raw, status)
		VALUES ($1, 'mockvendor', $2, 'ext-legacy', $3, 5000, 'IDR', '{"legacy":true}'::jsonb, 'received')`,
		id, uuid.NewString(), userID)
	require.NoError(t, err)

	repo := repository.NewRepository(db, payinTestRing(t))
	got, err := repo.Get(ctx, id)
	require.NoError(t, err, "a row with no raw_ciphertext must still be readable via the plaintext fallback")
	require.JSONEq(t, `{"legacy":true}`, string(got.Raw))
}

// TestPayinRepository_BackfillOnce_RestartableEqualTimestamps is docs/roadmap/active/51
// T2.5's own required test: pre-migration rows sharing an identical
// created_at all get backfilled exactly once across many small,
// restart-simulating BackfillOnce calls.
func TestPayinRepository_BackfillOnce_RestartableEqualTimestamps(t *testing.T) {
	db := setupPayinTestDB(t)
	ctx := context.Background()

	const rowCount = 20
	sharedCreatedAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]uuid.UUID, rowCount)
	for i := 0; i < rowCount; i++ {
		ids[i] = uuid.New()
		raw := fmt.Sprintf(`{"secret":"legacy-%d"}`, i)
		_, err := db.ExecContext(ctx, `
			INSERT INTO payin_webhook_events (id, vendor, vendor_event_id, external_ref, user_id, amount, currency, raw, status, created_at, updated_at)
			VALUES ($1, 'mockvendor', $2, 'ext-legacy', $3, 1000, 'IDR', $4::jsonb, 'received', $5, $5)`,
			ids[i], uuid.NewString(), uuid.New(), raw, sharedCreatedAt)
		require.NoError(t, err)
	}

	repo := repository.NewRepository(db, payinTestRing(t))
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
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM payin_webhook_events WHERE raw_ciphertext IS NULL`).Scan(&remaining))
	require.Zero(t, remaining, "no payin_webhook_events row may still be missing ciphertext after backfill completes")

	for i, id := range ids {
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		require.JSONEq(t, fmt.Sprintf(`{"secret":"legacy-%d"}`, i), string(got.Raw))
	}

	n, err := repo.BackfillOnce(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}
