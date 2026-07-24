//go:build integration

// Proves docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.4's K2/K3
// encryption (contract-migrated by "A8 T2.5b" — no plaintext fallback
// remains) for payin_webhook_events.raw end to end against a real
// Postgres: ciphertext round-trip, a nil ring refused at construction, and
// a row whose ciphertext T2.6's own retention redaction already nulled
// reading back as the redacted marker rather than erroring. Reuses
// setupPayinTestDB (payin_integration_test.go, same package).
package payin_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/internal/payin/repository"
	"github.com/herdifirdausss/seev/pkg/cryptox"
)

func payinTestRing(t *testing.T) *cryptox.Ring {
	t.Helper()
	require.NotNil(t, payinCryptoxTestRing)
	return payinCryptoxTestRing
}

// payinCryptoxTestRing is package-level (no *testing.T needed) so setup
// helpers like newPayinModule — which payin.NewModule now REQUIRES a real
// ring for, "A8 T2.5b" having removed the nil-ring-tolerant construction
// path entirely — can use it without threading t through every call site.
var payinCryptoxTestRing = mustBuildPayinTestRing()

func mustBuildPayinTestRing() *cryptox.Ring {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	if err != nil {
		panic(err)
	}
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

// TestPayinRepository_NilRing_PanicsAtConstruction is "A8 T2.5b"'s own
// required test: once payin_webhook_events.raw has no plaintext column, a
// missing ring can never degrade gracefully — it must fail loudly at
// construction, not nil-pointer somewhere inside a live request.
func TestPayinRepository_NilRing_PanicsAtConstruction(t *testing.T) {
	db := setupPayinTestDB(t)
	require.Panics(t, func() { repository.NewRepository(db, nil) })
}

// TestPayinRepository_RedactedRow_ReadsAsMarkerNotError proves a row
// T2.6's own retention redaction already nulled raw_ciphertext on (the
// SAME state the pre-contract plaintext column's own
// {"redacted":true} marker used to represent) reads back cleanly instead
// of erroring.
func TestPayinRepository_RedactedRow_ReadsAsMarkerNotError(t *testing.T) {
	db := setupPayinTestDB(t)
	ctx := context.Background()
	repo := repository.NewRepository(db, payinTestRing(t))

	ev := model.WebhookEvent{
		ID: uuid.New(), Vendor: "mockvendor", VendorEventID: uuid.NewString(),
		ExternalRef: "ext-redacted", UserID: uuid.New(), Amount: decimal.NewFromInt(10000), Currency: "IDR",
		Raw: []byte(`{"secret":"do-not-leak"}`),
	}
	_, err := repo.GetOrInsert(ctx, ev)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `UPDATE payin_webhook_events SET raw_ciphertext = NULL, raw_key_version = NULL WHERE id = $1`, ev.ID)
	require.NoError(t, err)

	got, err := repo.Get(ctx, ev.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{"redacted":true}`, string(got.Raw))
}
