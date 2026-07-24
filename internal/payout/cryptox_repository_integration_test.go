//go:build integration

// Proves docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.4's K2/K3
// encryption (contract-migrated by "A8 T2.5b" — no plaintext fallback
// remains) for payout_requests.destination end to end against a real
// Postgres: ciphertext round-trip, a nil ring refused at construction, and
// a row whose ciphertext T2.6's own retention redaction already nulled
// reading back as the redacted marker rather than erroring. Reuses
// setupPayoutTestDB (payout_integration_test.go, same package).
package payout_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/payout/repository"
	"github.com/herdifirdausss/seev/pkg/cryptox"
)

func payoutTestRing(t *testing.T) *cryptox.Ring {
	t.Helper()
	require.NotNil(t, payoutCryptoxTestRing)
	return payoutCryptoxTestRing
}

// payoutCryptoxTestRing is package-level (no *testing.T needed) so setup
// helpers that construct a payout.Module directly — which payout.NewModule
// now REQUIRES a real ring for, "A8 T2.5b" having removed the
// nil-ring-tolerant construction path entirely — can use it without
// threading t through every call site.
var payoutCryptoxTestRing = mustBuildPayoutTestRing()

func mustBuildPayoutTestRing() *cryptox.Ring {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 5)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	if err != nil {
		panic(err)
	}
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

// TestPayoutRepository_NilRing_PanicsAtConstruction is "A8 T2.5b"'s own
// required test: once payout_requests.destination has no plaintext
// column, a missing ring can never degrade gracefully — it must fail
// loudly at construction, not nil-pointer somewhere inside a live request.
func TestPayoutRepository_NilRing_PanicsAtConstruction(t *testing.T) {
	db := setupPayoutTestDB(t)
	require.Panics(t, func() { repository.NewRepository(db, nil) })
}

// TestPayoutRepository_RedactedRow_ReadsAsMarkerNotError proves a row
// T2.6's own retention redaction already nulled destination_ciphertext on
// (the SAME state the pre-contract plaintext column's own
// {"redacted":true} marker used to represent) reads back cleanly instead
// of erroring.
func TestPayoutRepository_RedactedRow_ReadsAsMarkerNotError(t *testing.T) {
	db := setupPayoutTestDB(t)
	ctx := context.Background()
	repo := repository.NewRepository(db, payoutTestRing(t))

	req := model.PayoutRequest{
		ID: uuid.New(), UserID: uuid.New(), Amount: decimal.NewFromInt(50000), Currency: "IDR",
		Vendor: "mockvendor", Destination: []byte(`{"account_no":"1234567890"}`), CreatedBy: "test",
	}
	require.NoError(t, repo.Insert(ctx, req))

	_, err := db.ExecContext(ctx, `UPDATE payout_requests SET destination_ciphertext = NULL, destination_key_version = NULL WHERE id = $1`, req.ID)
	require.NoError(t, err)

	got, err := repo.Get(ctx, req.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{"redacted":true}`, string(got.Destination))
}
