//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md "A8 T4b"/"A8 T5b" (K9, K10, K11) —
// the export and closure contracts built for the four owners deferred at
// T4/T5 (payin, payout, fraud, gateway/notify) — against a real Postgres
// and real in-process owner modules, each reached over a genuine HTTP
// round trip (same httptest.Server-wrapping-a-real-module pattern
// closure_integration_test.go already established for ledger).
package auth_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/auth"
	"github.com/herdifirdausss/seev/internal/fraud"
	"github.com/herdifirdausss/seev/internal/notify"
	"github.com/herdifirdausss/seev/internal/payin"
	"github.com/herdifirdausss/seev/internal/payout"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/cryptox"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

// setupMultiOwnerModule wires ALL five registered owners (ledger, payin,
// payout, fraud, gateway/notify) — every owner's own PrivacyRouter wrapped
// in its own httptest.Server, matching cmd/auth-service/main.go's own
// production wiring shape (one mTLS-bound client per owner) minus mTLS.
// The non-privacy dependencies each owner's own NewModule takes
// (vendor registry, poster, broker, breaker, Redis) are all nil/zero —
// safe because PrivacyPrepareClosure/PrivacyCommitClosure/PrivacyExportRows
// only ever touch each module's own `db` field.
func setupMultiOwnerModule(t *testing.T) (*auth.Module, *database.DBSQL, *cryptox.Ring) {
	t.Helper()
	db := setupAuthTestDB(t)
	ledgerHarness := testutil.NewLedgerHarness(db)
	payinModule := payin.NewModule(db, nil, nil, time.Hour, nil, nil, nil, cryptoxTestRing)
	payoutModule := payout.NewModule(db, nil, nil, nil, nil, nil, nil, cryptoxTestRing)
	fraudModule := fraud.NewModule(db, nil, nil, fraud.Config{}, nil)
	notifyModule := notify.NewModule(db, nil, nil)

	m := auth.NewModule(db, ledgerHarness, auth.Config{
		JWTSecret: testJWTSecretIT, JWTIssuer: "seev-test",
		AccessExpiry: 15 * time.Minute, RefreshExpiry: 7 * 24 * time.Hour,
		DefaultCurrency: "IDR",
	}, nil, cryptoxTestRing, cryptoxTestLookup)
	m.SetClosureKeyRing(testClosureRing(t))
	exportRing := testRing(t)
	m.SetExportKeyRing(exportRing)
	m.SetDocumentStore(newFakeDocStore())

	regOwner := func(name string, h http.Handler) {
		wrapped := middleware.WithInternalToken(testClosureInternalToken)(h)
		server := httptest.NewServer(wrapped)
		t.Cleanup(server.Close)
		m.RegisterClosureOwner(name, auth.NewOwnerClosureClient(server.URL, testClosureInternalToken, server.Client()))
	}

	regOwner("ledger", ledgerHarness.Module().ClosureRouter())
	regOwner("payin", payinModule.PrivacyRouter())
	regOwner("payout", payoutModule.PrivacyRouter())
	regOwner("fraud", fraudModule.PrivacyRouter())
	regOwner("gateway", notifyModule.PrivacyRouter())

	return m, db, exportRing
}

func TestMultiOwner_Closure_RepointsAllFourNewOwners(t *testing.T) {
	m, db, _ := setupMultiOwnerModule(t)
	ctx := context.Background()
	const password = "hunter22!"
	userID := registerTestUser(t, m, "multiowner-happy@example.test", password)

	// Seed one row per new owner directly (simplest, focused way to prove
	// the repoint — these modules' own business logic for CREATING these
	// rows is already tested elsewhere).
	webhookEventID := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO payin_webhook_events (id, vendor, vendor_event_id, external_ref, user_id, amount, total_debit, currency, status)
		VALUES ($1, 'mockvendor', 'evt-1', 'ref-1', $2, 50000, 50000, 'IDR', 'posted')`, webhookEventID, userID)
	require.NoError(t, err)
	payoutRequestID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO payout_requests (id, user_id, amount, currency, vendor, status, created_by)
		VALUES ($1, $2, 20000, 'IDR', 'mockvendor', 'settled', 'test')`, payoutRequestID, userID)
	require.NoError(t, err)

	screeningEventID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO screening_events (id, tx_type, user_id, amount, currency, rule, verdict, reason)
		VALUES ($1, 'transfer_p2p', $2, 999999999, 'IDR', 'amount_threshold', 'flagged', 'amount exceeds threshold')`, screeningEventID, userID)
	require.NoError(t, err)

	notificationID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO notif_notifications (id, user_id, event_id, type, title, body)
		VALUES ($1, $2, $3, 'money_in', 'Top-up received', 'Your top-up has settled.')`, notificationID, userID, uuid.New())
	require.NoError(t, err)

	req, err := m.RequestClosure(ctx, userID, password)
	require.NoError(t, err)

	status := driveClosureToCompletion(t, m, db, req.ID, 20)
	require.Equal(t, "completed", status)

	var surrogateID uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx, `SELECT surrogate_id FROM privacy_requests WHERE id = $1`, req.ID).Scan(&surrogateID))
	require.NotEqual(t, uuid.Nil, surrogateID)

	for _, tc := range []struct {
		table string
		id    uuid.UUID
	}{
		{"payin_webhook_events", webhookEventID},
		{"payout_requests", payoutRequestID},
		{"screening_events", screeningEventID},
		{"notif_notifications", notificationID},
	} {
		var ownerID uuid.UUID
		require.NoError(t, db.QueryRowContext(ctx, `SELECT user_id FROM `+tc.table+` WHERE id = $1`, tc.id).Scan(&ownerID), tc.table)
		require.Equal(t, surrogateID, ownerID, "%s row must be repointed to the surrogate", tc.table)
	}

	// Every owner's checkpoint must be recorded 'committed'.
	var checkpointsRaw []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT owner_checkpoints FROM privacy_requests WHERE id = $1`, req.ID).Scan(&checkpointsRaw))
	var checkpoints map[string]struct {
		Phase string `json:"phase"`
	}
	require.NoError(t, json.Unmarshal(checkpointsRaw, &checkpoints))
	for _, owner := range []string{"ledger", "payin", "payout", "fraud", "gateway"} {
		require.Equal(t, "committed", checkpoints[owner].Phase, "missing/wrong checkpoint for owner %s", owner)
	}
}

// TestMultiOwner_Closure_PendingTopupIntentBlocks is T5b's own required
// test: payin's K10 blocking condition (an open top-up in flight).
func TestMultiOwner_Closure_PendingTopupIntentBlocks(t *testing.T) {
	m, db, _ := setupMultiOwnerModule(t)
	ctx := context.Background()
	const password = "hunter22!"
	userID := registerTestUser(t, m, "multiowner-payin-block@example.test", password)

	_, err := db.ExecContext(ctx, `
		INSERT INTO payin_topup_intents (id, reference, user_id, amount, total_debit, currency, vendor, status, expires_at)
		VALUES ($1, 'ref-pending', $2, 10000, 10000, 'IDR', 'mockvendor', 'pending', now() + interval '1 hour')`, uuid.New(), userID)
	require.NoError(t, err)

	req, err := m.RequestClosure(ctx, userID, password)
	require.NoError(t, err)

	status := driveClosureToCompletion(t, m, db, req.ID, 5)
	require.Equal(t, "blocked", status)

	var lastError string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COALESCE(last_error,'') FROM privacy_requests WHERE id = $1`, req.ID).Scan(&lastError))
	require.Contains(t, lastError, "topup intent")
}

// TestMultiOwner_Closure_OpenPayoutRequestBlocks is T5b's own required
// test: payout's K10 blocking condition (an open withdrawal lifecycle).
func TestMultiOwner_Closure_OpenPayoutRequestBlocks(t *testing.T) {
	m, db, _ := setupMultiOwnerModule(t)
	ctx := context.Background()
	const password = "hunter22!"
	userID := registerTestUser(t, m, "multiowner-payout-block@example.test", password)

	_, err := db.ExecContext(ctx, `
		INSERT INTO payout_requests (id, user_id, amount, currency, vendor, status, created_by)
		VALUES ($1, $2, 15000, 'IDR', 'mockvendor', 'held', 'test')`, uuid.New(), userID)
	require.NoError(t, err)

	req, err := m.RequestClosure(ctx, userID, password)
	require.NoError(t, err)

	status := driveClosureToCompletion(t, m, db, req.ID, 5)
	require.Equal(t, "blocked", status)

	var lastError string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COALESCE(last_error,'') FROM privacy_requests WHERE id = $1`, req.ID).Scan(&lastError))
	require.Contains(t, lastError, "open withdrawal lifecycle")
}

func TestPrivacyExportWorkerDoesNotClaimPendingClosure(t *testing.T) {
	m, db, _ := setupMultiOwnerModule(t)
	ctx := context.Background()
	const password = "hunter22!"
	userID := registerTestUser(t, m, "closure-not-export@example.test", password)

	req, err := m.RequestClosure(ctx, userID, password)
	require.NoError(t, err)
	require.NoError(t, m.AssembleOnePendingExport(ctx))

	var requestType, status string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT request_type, status FROM privacy_requests WHERE id = $1`, req.ID,
	).Scan(&requestType, &status))
	require.Equal(t, "closure", requestType)
	require.Equal(t, "pending", status, "the export worker must never claim a closure request")
}

// TestMultiOwner_Export_IncludesAllRegisteredOwners is T4b's own required
// test: the manifest lists every registered owner (not just auth), and
// each owner's NDJSON contains ONLY the subject's own row.
func TestMultiOwner_Export_IncludesAllRegisteredOwners(t *testing.T) {
	m, db, _ := setupMultiOwnerModule(t)
	ctx := context.Background()
	const password = "hunter22!"
	userID := registerTestUser(t, m, "multiowner-export@example.test", password)
	otherUserID := registerTestUser(t, m, "multiowner-export-other@example.test", password)

	ownEventID := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO payin_webhook_events (id, vendor, vendor_event_id, external_ref, user_id, amount, total_debit, currency, status)
		VALUES ($1, 'mockvendor', 'evt-export', 'ref-export', $2, 75000, 75000, 'IDR', 'posted')`, ownEventID, userID)
	require.NoError(t, err)
	// Cross the coordinator's 100-row page boundary. This proves it follows
	// next_cursor until exhaustion instead of silently truncating one owner.
	for i := 0; i < 105; i++ {
		_, err = db.ExecContext(ctx, `
			INSERT INTO payin_webhook_events (id, vendor, vendor_event_id, external_ref, user_id, amount, total_debit, currency, status)
			VALUES ($1, 'mockvendor', $2, $3, $4, 75000, 75000, 'IDR', 'posted')`,
			uuid.New(), fmt.Sprintf("evt-page-%03d", i), fmt.Sprintf("ref-page-%03d", i), userID)
		require.NoError(t, err)
	}
	// Another user's row must never leak into the subject's own export.
	otherEventID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO payin_webhook_events (id, vendor, vendor_event_id, external_ref, user_id, amount, total_debit, currency, status)
		VALUES ($1, 'mockvendor', 'evt-other', 'ref-other', $2, 1, 1, 'IDR', 'posted')`, otherEventID, otherUserID)
	require.NoError(t, err)

	req, err := m.RequestExport(ctx, userID, password)
	require.NoError(t, err)
	require.NoError(t, m.AssembleOnePendingExport(ctx))

	got, err := m.GetExportStatus(ctx, userID, req.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", got.Status)

	plaintext, err := m.DownloadExport(ctx, userID, req.ID, password)
	require.NoError(t, err)

	zr, err := zip.NewReader(bytes.NewReader(plaintext), int64(len(plaintext)))
	require.NoError(t, err)
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err)
		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		rc.Close()
		files[f.Name] = data
	}

	var manifest struct {
		Owners []struct {
			Owner    string `json:"owner"`
			RowCount int    `json:"row_count"`
		} `json:"owners"`
	}
	require.NoError(t, json.Unmarshal(files["manifest.json"], &manifest))
	rowCounts := map[string]int{}
	for _, o := range manifest.Owners {
		rowCounts[o.Owner] = o.RowCount
	}
	for _, owner := range []string{"auth", "ledger", "payin", "payout", "fraud", "gateway"} {
		_, present := rowCounts[owner]
		require.True(t, present, "manifest must list every registered owner, missing %s", owner)
	}
	require.Equal(t, 106, rowCounts["payin"], "all payin rows across two pages must be present")

	require.Contains(t, files, "payin.ndjson")
	payinNDJSON := string(files["payin.ndjson"])
	require.Contains(t, payinNDJSON, ownEventID.String())
	require.NotContains(t, payinNDJSON, otherEventID.String(), "another user's payin row must never leak into the subject's own export")
}
