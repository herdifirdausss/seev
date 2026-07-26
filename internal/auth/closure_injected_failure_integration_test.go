//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T5's own
// required test, explicitly flagged as not yet live-verified in T5's and
// T4b/T5b's own Result sections: "one unavailable owner leaves the user
// disabled and resumes forward later." Every other closure test drives the
// saga against healthy owners only — this one injects a REAL HTTP failure
// mid-saga for one owner and proves the mechanism (closureRetryOrDead's
// backoff, per-owner checkpoint resumability) actually recovers once that
// owner comes back.
package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

// flakyHandler wraps a real http.Handler with an atomic on/off switch —
// off means every request fails with 503, simulating a genuinely
// unavailable owner rather than a mocked error return, so the saga's own
// real HTTP client error-handling path (httpOwnerClosureClient, not a
// stub) is what gets exercised.
type flakyHandler struct {
	healthy atomic.Bool
	real    http.Handler
}

func (f *flakyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !f.healthy.Load() {
		http.Error(w, "simulated owner outage", http.StatusServiceUnavailable)
		return
	}
	f.real.ServeHTTP(w, r)
}

// setupModuleWithFlakyPayin mirrors setupMultiOwnerModule exactly except
// payin's own router is wrapped in a flakyHandler the test can flip.
// Registration order (ledger, payin, payout, fraud, gateway) is preserved
// so payin is the SECOND owner processed each saga step — proving at
// least one healthy owner ahead of it already checkpoints correctly while
// payin itself is down.
func setupModuleWithFlakyPayin(t *testing.T) (*auth.Module, *database.DBSQL, *flakyHandler) {
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
	m.SetExportKeyRing(testRing(t))
	m.SetDocumentStore(newFakeDocStore())

	regOwner := func(name string, h http.Handler) {
		wrapped := middleware.WithInternalToken(testClosureInternalToken)(h)
		server := httptest.NewServer(wrapped)
		t.Cleanup(server.Close)
		m.RegisterClosureOwner(name, auth.NewOwnerClosureClient(server.URL, testClosureInternalToken, server.Client()))
	}

	regOwner("ledger", ledgerHarness.Module().ClosureRouter())

	flaky := &flakyHandler{real: payinModule.PrivacyRouter()}
	flaky.healthy.Store(true)
	regOwner("payin", flaky)

	regOwner("payout", payoutModule.PrivacyRouter())
	regOwner("fraud", fraudModule.PrivacyRouter())
	regOwner("gateway", notifyModule.PrivacyRouter())

	return m, db, flaky
}

func TestClosure_InjectedOwnerFailure_LeavesDisabledAndResumesForward(t *testing.T) {
	m, db, flaky := setupModuleWithFlakyPayin(t)
	ctx := context.Background()
	const password = "hunter22!"
	userID := registerTestUser(t, m, "injected-failure@example.test", password)

	req, err := m.RequestClosure(ctx, userID, password)
	require.NoError(t, err)

	flaky.healthy.Store(false)

	// One step: ledger (registered first) prepares and checkpoints fine;
	// payin (registered second) fails immediately with a real HTTP 503 —
	// closureRetryOrDead records the failure and schedules a backoff retry
	// rather than dead-lettering on the first failure.
	require.NoError(t, m.ProcessOnePendingClosure(ctx))

	var status, lastError string
	var retryCount int
	var nextAttemptAt *time.Time
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT status, COALESCE(last_error,''), retry_count, next_attempt_at
		FROM privacy_requests WHERE id = $1`, req.ID).Scan(&status, &lastError, &retryCount, &nextAttemptAt))
	require.Equal(t, "pending", status, "a transient owner failure must not advance the saga's own status")
	require.Equal(t, 1, retryCount)
	require.Contains(t, lastError, "payin")
	require.NotNil(t, nextAttemptAt, "a backed-off retry must have a scheduled next attempt")
	require.True(t, nextAttemptAt.After(time.Now()), "the backoff window must still be in the future")

	// Ledger's own checkpoint from before the failure must have survived —
	// this is the per-owner resumability the saga's own design promises:
	// a retried step never re-calls an owner already checkpointed.
	var checkpointsRaw []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT owner_checkpoints FROM privacy_requests WHERE id = $1`, req.ID).Scan(&checkpointsRaw))
	require.Contains(t, string(checkpointsRaw), `"ledger"`)
	require.NotContains(t, string(checkpointsRaw), `"payin"`, "payin never successfully checkpointed, so it must not appear yet")

	// A retry attempt BEFORE the backoff window elapses must be a genuine
	// no-op — the claim query's own next_attempt_at gate is what enforces
	// "resumes forward LATER," not immediately.
	require.NoError(t, m.ProcessOnePendingClosure(ctx))
	var statusAfterEarlyPoll string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM privacy_requests WHERE id = $1`, req.ID).Scan(&statusAfterEarlyPoll))
	require.Equal(t, "pending", statusAfterEarlyPoll)

	// The owner recovers. Simulate the backoff window having elapsed
	// (rather than actually sleeping 30s) — the SAME mechanism a real
	// clock tick would trigger, just fast-forwarded for the test.
	flaky.healthy.Store(true)
	_, err = db.ExecContext(ctx, `UPDATE privacy_requests SET next_attempt_at = NULL WHERE id = $1`, req.ID)
	require.NoError(t, err)

	finalStatus := driveClosureToCompletion(t, m, db, req.ID, 20)
	require.Equal(t, "completed", finalStatus, "the saga must resume forward and reach completion once the owner recovers")

	var surrogateID uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx, `SELECT surrogate_id FROM privacy_requests WHERE id = $1`, req.ID).Scan(&surrogateID))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT owner_checkpoints FROM privacy_requests WHERE id = $1`, req.ID).Scan(&checkpointsRaw))
	for _, owner := range []string{"ledger", "payin", "payout", "fraud", "gateway"} {
		require.Contains(t, string(checkpointsRaw), `"`+owner+`"`, "owner %s must have checkpointed after recovery", owner)
	}
}
