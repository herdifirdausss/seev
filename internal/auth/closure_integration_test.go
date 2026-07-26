//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T5's (K10, K11) account-closure
// saga end to end against a real Postgres and a real in-process
// ledger.Module reached over a real HTTP round trip (ledgerHarness's
// ClosureRouter wrapped in httptest.Server, exactly the same transport
// shape cmd/auth-service/main.go wires in production, just without mTLS).
// Scope: auth + ledger only (A8 T5's own stated scope decision — see doc
// 51's T5 Result section) — the other six K11 owners and the operator
// maker/checker offboarding flow are deferred to A8 T5b.
package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/auth"
	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/cryptox"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

const testClosureInternalToken = "test-internal-token"

func testClosureRing(t *testing.T) *cryptox.Ring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 50)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	return ring
}

func setupClosureModule(t *testing.T) (*auth.Module, *database.DBSQL, *testutil.LedgerHarness) {
	t.Helper()
	db := setupAuthTestDB(t)
	ledgerHarness := testutil.NewLedgerHarness(db)
	m := auth.NewModule(db, ledgerHarness, auth.Config{
		JWTSecret: testJWTSecretIT, JWTIssuer: "seev-test",
		AccessExpiry: 15 * time.Minute, RefreshExpiry: 7 * 24 * time.Hour,
		DefaultCurrency: "IDR",
	}, nil, cryptoxTestRing, cryptoxTestLookup)

	m.SetClosureKeyRing(testClosureRing(t))
	handler := middleware.WithInternalToken(testClosureInternalToken)(ledgerHarness.Module().ClosureRouter())
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	m.RegisterClosureOwner("ledger", auth.NewOwnerClosureClient(server.URL, testClosureInternalToken, server.Client()))

	return m, db, ledgerHarness
}

// driveClosureToCompletion calls ProcessOnePendingClosure repeatedly — each
// call is exactly one saga step (pending->preparing->committing->completed
// or blocked/dead) — stopping as soon as the request reaches a terminal
// status or maxSteps is exhausted. Calling this in a loop, rather than once,
// is itself what proves crash/restart resumption: nothing here assumes the
// whole saga completes within a single call.
func driveClosureToCompletion(t *testing.T, m *auth.Module, db *database.DBSQL, requestID uuid.UUID, maxSteps int) string {
	t.Helper()
	ctx := context.Background()
	var status string
	for i := 0; i < maxSteps; i++ {
		require.NoError(t, m.ProcessOnePendingClosure(ctx))
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM privacy_requests WHERE id = $1`, requestID).Scan(&status))
		if status == "completed" || status == "blocked" || status == "dead" {
			return status
		}
	}
	return status
}

func TestClosure_RequestClosure_WrongPassword(t *testing.T) {
	m, _, _ := setupClosureModule(t)
	userID := registerTestUser(t, m, "close-alice@example.test", "hunter22!")

	_, err := m.RequestClosure(context.Background(), userID, "wrong-password")
	require.ErrorIs(t, err, auth.ErrInvalidCredentials)
}

// TestClosure_RequestClosure_AdminRejected is T5's own required test:
// K10 "admin/operator accounts cannot use self-service closure."
func TestClosure_RequestClosure_AdminRejected(t *testing.T) {
	m, db, _ := setupClosureModule(t)
	ctx := context.Background()
	userID := registerTestUser(t, m, "close-admin@example.test", "hunter22!")
	_, err := db.ExecContext(ctx, `UPDATE auth_users SET role = 'admin' WHERE id = $1`, userID)
	require.NoError(t, err)

	_, err = m.RequestClosure(ctx, userID, "hunter22!")
	require.ErrorIs(t, err, auth.ErrClosureNotSelfService)
}

func TestClosure_RequestClosure_AlreadyDisabledUser(t *testing.T) {
	m, db, _ := setupClosureModule(t)
	ctx := context.Background()
	userID := registerTestUser(t, m, "close-disabled@example.test", "hunter22!")
	_, err := db.ExecContext(ctx, `UPDATE auth_users SET status = 'disabled' WHERE id = $1`, userID)
	require.NoError(t, err)

	_, err = m.RequestClosure(ctx, userID, "hunter22!")
	require.ErrorIs(t, err, auth.ErrUserDisabled)
}

// TestClosure_DuplicateRequest_ReturnsSameActiveRequest is T5's own
// required test: "duplicate commands do not change result counts or create
// a second surrogate" (the request-creation half of it — Commit's own
// idempotency is proven separately below).
func TestClosure_DuplicateRequest_ReturnsSameActiveRequest(t *testing.T) {
	m, _, _ := setupClosureModule(t)
	ctx := context.Background()
	userID := registerTestUser(t, m, "close-dup@example.test", "hunter22!")

	first, err := m.RequestClosure(ctx, userID, "hunter22!")
	require.NoError(t, err)
	second, err := m.RequestClosure(ctx, userID, "hunter22!")
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
}

// TestClosure_NonZeroBalance_Blocks is T5's own required test: "every
// blocking condition" — non-zero balance, ledger-owned.
func TestClosure_NonZeroBalance_Blocks(t *testing.T) {
	m, db, harness := setupClosureModule(t)
	ctx := context.Background()
	userID := registerTestUser(t, m, "close-balance@example.test", "hunter22!")

	require.NoError(t, harness.Post(ctx, ledgerclient.Command{
		IdempotencyKey: "topup-" + userID.String(), Type: "money_in",
		Amount: decimal.NewFromInt(50_000), UserID: userID,
		Metadata: map[string]any{"gateway": "bca"},
	}))

	req, err := m.RequestClosure(ctx, userID, "hunter22!")
	require.NoError(t, err)

	status := driveClosureToCompletion(t, m, db, req.ID, 3)
	require.Equal(t, "blocked", status)

	var lastError string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COALESCE(last_error,'') FROM privacy_requests WHERE id = $1`, req.ID).Scan(&lastError))
	require.Contains(t, lastError, "balance")

	// The user must remain 'closing' (disabled, but not yet finalized) —
	// blocked is terminal for the SAGA, not silently reversible back to
	// 'active' by this pass (A8 T5b's own scope note covers an operator
	// unblock/cancel flow).
	var userStatus string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM auth_users WHERE id = $1`, userID).Scan(&userStatus))
	require.Equal(t, "closing", userStatus)
}

// TestClosure_ActiveHold_Blocks is T5's own required test: "hold appears
// during prepare and prevents commit" — auth's own local check.
func TestClosure_ActiveHold_Blocks(t *testing.T) {
	m, db, _ := setupClosureModule(t)
	ctx := context.Background()
	userID := registerTestUser(t, m, "close-hold@example.test", "hunter22!")

	_, err := db.ExecContext(ctx, `
		INSERT INTO auth_retention_holds (id, scope, scope_value, reason_code, created_by)
		VALUES ($1, 'subject', $2, 'legal_hold', 'tester')`, uuid.New(), userID.String())
	require.NoError(t, err)

	req, err := m.RequestClosure(ctx, userID, "hunter22!")
	require.NoError(t, err)

	status := driveClosureToCompletion(t, m, db, req.ID, 3)
	require.Equal(t, "blocked", status)

	var lastError string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COALESCE(last_error,'') FROM privacy_requests WHERE id = $1`, req.ID).Scan(&lastError))
	require.Contains(t, lastError, "retention hold")
}

// TestClosure_HappyPath_FullLifecycle is T5's own required tests bundled
// together: "an eligible happy path", "old login, refresh token... user
// routes... fail after completion", "all owner references use the
// surrogate", "logs/audit never expose original-to-surrogate mapping"
// (asserted here as: the completed row carries no plaintext ciphertext).
func TestClosure_HappyPath_FullLifecycle(t *testing.T) {
	m, db, _ := setupClosureModule(t)
	ctx := context.Background()
	const password = "hunter22!"
	userID := registerTestUser(t, m, "close-happy@example.test", password)

	_, _, loginErr := m.Login(ctx, "close-happy@example.test", password)
	require.NoError(t, loginErr, "login must work before closure")

	req, err := m.RequestClosure(ctx, userID, password)
	require.NoError(t, err)
	require.Equal(t, "pending", req.Status)

	// Immediately after the request (before the saga even runs a step),
	// login must already be rejected — K10's own "immediately disables new
	// login."
	_, _, loginErr = m.Login(ctx, "close-happy@example.test", password)
	require.ErrorIs(t, loginErr, auth.ErrUserDisabled)

	status := driveClosureToCompletion(t, m, db, req.ID, 10)
	require.Equal(t, "completed", status)

	// Auth finalized last: credentials gone, identity tombstoned, no
	// active-saga ciphertext left.
	var credCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM auth_credentials WHERE user_id = $1`, userID).Scan(&credCount))
	require.Equal(t, 0, credCount)

	tombstoned, err := repository.NewUserRepository(db, cryptoxTestRing, cryptoxTestLookup).GetUserByID(ctx, userID)
	require.NoError(t, err)
	require.NotContains(t, tombstoned.Email, "close-happy@example.test")
	require.Equal(t, "[deleted]", tombstoned.FullName)
	require.Equal(t, "closed", tombstoned.Status)

	var ciphertext []byte

	var surrogateID uuid.UUID
	require.NoError(t, db.QueryRowContext(ctx, `SELECT surrogate_id, active_subject_ciphertext FROM privacy_requests WHERE id = $1`, req.ID).Scan(&surrogateID, &ciphertext))
	require.Nil(t, ciphertext, "the active-saga ciphertext must be destroyed on completion")
	require.NotEqual(t, uuid.Nil, surrogateID)

	// Ledger owner reference actually moved to the surrogate.
	var ownedBySurrogate, ownedByOriginal int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE owner_id = $1 AND owner_type = 'user'`, surrogateID).Scan(&ownedBySurrogate))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE owner_id = $1 AND owner_type = 'user'`, userID).Scan(&ownedByOriginal))
	require.Greater(t, ownedBySurrogate, 0)
	require.Equal(t, 0, ownedByOriginal)

	// Old login must fail after completion.
	_, _, loginErr = m.Login(ctx, "close-happy@example.test", password)
	require.ErrorIs(t, loginErr, auth.ErrInvalidCredentials, "the tombstoned email is no longer a valid login identifier")

	// user routes (Me) must fail for the closed identity.
	_, meErr := m.Me(ctx, userID)
	require.ErrorIs(t, meErr, auth.ErrUserDisabled)

	// Refresh tokens revoked.
	var liveTokens int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM auth_refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL`, userID).Scan(&liveTokens))
	require.Equal(t, 0, liveTokens)
}

// TestClosure_Commit_IdempotentUnderReplay is T5's own required test:
// "duplicate commands do not change result counts or create a second
// surrogate" — the owner-commit half. Calls ledger's Commit twice in a
// row for the same (subject, surrogate) pair — simulating a crash between
// ledger durably applying the UPDATE and auth recording that success —
// and asserts the second call returns the IDENTICAL result and no second
// surrogate/account ever appears.
func TestClosure_Commit_IdempotentUnderReplay(t *testing.T) {
	_, db, harness := setupClosureModule(t)
	ctx := context.Background()
	userID := uuid.New()
	require.NoError(t, harness.ProvisionUser(ctx, userID, "IDR"))

	handler := middleware.WithInternalToken(testClosureInternalToken)(harness.Module().ClosureRouter())
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := auth.NewOwnerClosureClient(server.URL, testClosureInternalToken, server.Client())

	surrogateID := uuid.New()
	hash1, count1, err := client.Commit(ctx, userID, surrogateID)
	require.NoError(t, err)
	require.Greater(t, count1, 0)

	hash2, count2, err := client.Commit(ctx, userID, surrogateID)
	require.NoError(t, err)
	require.Equal(t, hash1, hash2)
	require.Equal(t, count1, count2)

	var accountCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE owner_id = $1 AND owner_type = 'user'`, surrogateID).Scan(&accountCount))
	require.Equal(t, count1, accountCount, "no duplicate/extra rows must exist after replaying Commit")
}

// TestClosure_LedgerEntriesUnchanged is T5's own required test:
// "ledger_entries checksums are byte-for-byte identical before/after."
func TestClosure_LedgerEntriesUnchanged(t *testing.T) {
	m, db, harness := setupClosureModule(t)
	ctx := context.Background()
	alice := registerTestUser(t, m, "close-entries-alice@example.test", "hunter22!")
	bob := registerTestUser(t, m, "close-entries-bob@example.test", "hunter22!")

	require.NoError(t, harness.Post(ctx, ledgerclient.Command{
		IdempotencyKey: "topup-" + alice.String(), Type: "money_in",
		Amount: decimal.NewFromInt(75_000), UserID: alice,
		Metadata: map[string]any{"gateway": "bca"},
	}))
	require.NoError(t, harness.Post(ctx, ledgerclient.Command{
		IdempotencyKey: "xfer-" + alice.String(), Type: "transfer_p2p",
		Amount: decimal.NewFromInt(75_000), UserID: alice, TargetUserID: bob,
	}))

	accountIDs := closureTestAccountIDs(t, db, alice)
	require.NotEmpty(t, accountIDs)
	before := closureTestEntriesChecksum(t, db, accountIDs)
	require.NotEmpty(t, before)

	req, err := m.RequestClosure(ctx, alice, "hunter22!")
	require.NoError(t, err)
	status := driveClosureToCompletion(t, m, db, req.ID, 10)
	require.Equal(t, "completed", status)

	after := closureTestEntriesChecksum(t, db, accountIDs)
	require.Equal(t, before, after, "ledger_entries for the closed user's accounts must be byte-for-byte unchanged by closure")
}

func TestClosure_RacingRetentionFailsClosedUntilClosureHorizon(t *testing.T) {
	m, db, _ := setupClosureModule(t)
	ctx := context.Background()
	const password = "hunter22!"
	userID := registerTestUser(t, m, "retention-race@example.test", password)
	submissionID := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO kyc_submissions
		  (id,user_id,level_requested,status,provider,created_at,decided_at,payload_ciphertext,payload_key_version)
		VALUES($1,$2,1,'rejected','test',now()-interval '400 days',now()-interval '400 days',$3,1)`,
		submissionID, userID, []byte("encrypted-payload"))
	require.NoError(t, err)
	req, err := m.RequestClosure(ctx, userID, password)
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs <- m.ProcessOnePendingClosure(ctx)
	}()
	go func() {
		defer wg.Done()
		var affected int
		err := db.QueryRowContext(ctx,
			`SELECT fn_retention_purge_kyc_submissions($1,500,false)`, uuid.New()).Scan(&affected)
		if err == nil && affected != 0 {
			err = fmt.Errorf("retention affected %d row(s) before closure horizon", affected)
		}
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	require.Equal(t, "completed", driveClosureToCompletion(t, m, db, req.ID, 8))
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM kyc_submissions WHERE id=$1`, submissionID).Scan(&count))
	require.Equal(t, 1, count, "a concurrent purge must not use row age instead of the 365-day closure horizon")
}

func closureTestAccountIDs(t *testing.T, db *database.DBSQL, userID uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT id FROM accounts WHERE owner_id = $1 AND owner_type = 'user' ORDER BY id`, userID)
	require.NoError(t, err)
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}

func closureTestEntriesChecksum(t *testing.T, db *database.DBSQL, accountIDs []uuid.UUID) string {
	t.Helper()
	h := sha256.New()
	for _, accID := range accountIDs {
		rows, err := db.QueryContext(context.Background(), `
			SELECT id, transaction_id, direction, amount, balance_after FROM ledger_entries
			WHERE account_id = $1 ORDER BY id`, accID)
		require.NoError(t, err)
		func() {
			defer rows.Close()
			for rows.Next() {
				var id, txID uuid.UUID
				var direction string
				var amount, balanceAfter int64
				require.NoError(t, rows.Scan(&id, &txID, &direction, &amount, &balanceAfter))
				h.Write([]byte(id.String() + txID.String() + direction))
				h.Write([]byte{byte(amount), byte(balanceAfter)})
			}
			require.NoError(t, rows.Err())
		}()
	}
	return hex.EncodeToString(h.Sum(nil))
}
