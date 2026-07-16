//go:build integration

// Package payin_test drives internal/payin.Module.HandleWebhook end to end
// against a real ledger.Module and real Postgres (docs/plan/22 Task T2) —
// proves the whole vertical: signature verification -> dedup -> money_in
// posting -> balance change -> recon-ready metadata, not just each piece in
// isolation.
package payin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/payin"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/internal/vendorgw/mockvendor"
	"github.com/herdifirdausss/seev/pkg/database"
)

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}

func setupPayinTestDB(t *testing.T) *database.DBSQL {
	t.Helper()
	ctx := context.Background()

	const dbName, dbUser, dbPassword = "seev_test", "test", "secret"

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5432")
	require.NoError(t, err)

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		dbUser, dbPassword, host, port.Port(), dbName)

	require.NoError(t, testutil.ApplyServiceMigrations(migrationsSourceURL(t), dsn))

	cfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	}
	db, err := database.New(ctx, cfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

// createUserCashAccount inserts a user 'cash' account + zero balance row
// directly via SQL — mirrors internal/ledger's own schema_contract_test.go
// helper.
func createUserCashAccount(t *testing.T, db *database.DBSQL, userID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	accountID := uuid.New()

	_, err := db.ExecContext(ctx, `
		INSERT INTO accounts (id, owner_id, owner_type, type, currency, status, created_by)
		VALUES ($1, $2, 'user', 'cash', 'IDR', 'active', 'payin_integration_test')`,
		accountID, userID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `INSERT INTO account_balances (account_id) VALUES ($1)`, accountID)
	require.NoError(t, err)

	return accountID
}

func getBalance(t *testing.T, db *database.DBSQL, accountID uuid.UUID) decimal.Decimal {
	t.Helper()
	var balance int64
	err := db.QueryRowContext(context.Background(),
		`SELECT balance FROM account_balances WHERE account_id = $1`, accountID).Scan(&balance)
	require.NoError(t, err)
	return decimal.NewFromInt(balance)
}

const mockSecret = "test-mock-secret"

// newPayinModule wires a real ledger.Module (no workers started — broker/
// redis/policy are nil, which is safe: Post never touches them, only
// StartWorkers does) and a real payin.Module with mockvendor registered.
func newPayinModule(db *database.DBSQL) *payin.Module {
	ledgerModule := testutil.NewLedgerHarness(db)

	registry := vendorgw.NewRegistry()
	registry.AddPayin(mockvendor.New(mockSecret))

	return payin.NewModule(db, ledgerModule, registry, 0, nil, nil)
}

func settledWebhookBody(eventID, externalRef string, userID uuid.UUID, amount int64) []byte {
	body, _ := json.Marshal(map[string]any{
		"event_id":     eventID,
		"external_ref": externalRef,
		"user_id":      userID.String(),
		"amount":       fmt.Sprintf("%d", amount),
		"currency":     "IDR",
		"occurred_at":  "2026-07-13T00:00:00Z",
		"type":         "payment.settled",
	})
	return body
}

func signedHeaders(body []byte) http.Header {
	h := http.Header{}
	h.Set(mockvendor.SignatureHeader, mockvendor.Sign(mockSecret, body))
	return h
}

type routeOnlyVerifier struct{ vendor string }

func (v routeOnlyVerifier) Vendor() string { return v.vendor }
func (v routeOnlyVerifier) VerifyAndParse(http.Header, []byte) (*vendorgw.PayinEvent, error) {
	return nil, nil
}

func TestPayin_CreateTopupIntent_UsesDatabaseRoutingRule(t *testing.T) {
	db := setupPayinTestDB(t)
	ledgerModule := testutil.NewLedgerHarness(db)
	registry := vendorgw.NewRegistry()
	registry.AddPayin(mockvendor.New(mockSecret))
	registry.AddPayin(routeOnlyVerifier{vendor: "priorityvendor"})
	m := payin.NewModule(db, ledgerModule, registry, 0, nil, nil)

	ctx := context.Background()
	userID := uuid.New()
	createUserCashAccount(t, db, userID)
	_, err := db.ExecContext(ctx, `INSERT INTO payin_vendor_gateways (vendor, gateway) VALUES ('priorityvendor', 'gopay')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO payin_routing_rules (id, flow, priority, currency, min_amount, max_amount, vendor) VALUES ($1, 'topup', 10, 'IDR', 100000, 300000, 'priorityvendor')`, uuid.New())
	require.NoError(t, err)

	intent, err := m.CreateTopupIntent(ctx, userID, decimal.NewFromInt(250_000))
	require.NoError(t, err)
	assert.Equal(t, "priorityvendor", intent.Vendor)
}

// TestPayin_HandleWebhook_EndToEnd_PostsMoneyInAndUpdatesBalance proves the
// full vertical: a validly-signed settled webhook results in a real
// money_in posting, a balance increase, and ledger metadata
// (external_ref/gateway) that recon (16-T2) can match against.
func TestPayin_HandleWebhook_EndToEnd_PostsMoneyInAndUpdatesBalance(t *testing.T) {
	db := setupPayinTestDB(t)
	m := newPayinModule(db)
	ctx := context.Background()

	userID := uuid.New()
	cash := createUserCashAccount(t, db, userID)

	body := settledWebhookBody("evt-e2e-1", "ref-e2e-1", userID, 250_000)
	err := m.HandleWebhook(ctx, mockvendor.VendorName, signedHeaders(body), body)
	require.NoError(t, err)

	assert.True(t, getBalance(t, db, cash).Equal(decimal.NewFromInt(250_000)))

	var status, externalRef, gateway string
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT status, external_ref, gateway FROM ledger_transactions
		WHERE idempotency_key = $1`, "payin:mockvendor:evt-e2e-1",
	).Scan(&status, &externalRef, &gateway))
	assert.Equal(t, "posted", status)
	assert.Equal(t, "ref-e2e-1", externalRef, "external_ref must be persisted so recon CSV (16-T2) can match this transaction")
	assert.Equal(t, "bca", gateway)

	rows, err := db.QueryContext(ctx, `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity')`)
	require.NoError(t, err)
	defer rows.Close()
	assert.False(t, rows.Next(), "fn_verify_ledger_balance found an unbalanced transaction")
}

// TestPayin_HandleWebhook_ConcurrentDuplicateDelivery_ExactlyOneMoneyIn
// proves the dedup guarantee under a real race (docs/plan/22 Task T2 DoD):
// the same webhook delivered by N concurrent goroutines results in exactly
// one money_in posting and one balance increase, not N.
func TestPayin_HandleWebhook_ConcurrentDuplicateDelivery_ExactlyOneMoneyIn(t *testing.T) {
	db := setupPayinTestDB(t)
	m := newPayinModule(db)
	ctx := context.Background()

	userID := uuid.New()
	cash := createUserCashAccount(t, db, userID)

	body := settledWebhookBody("evt-dup-1", "ref-dup-1", userID, 100_000)
	headers := signedHeaders(body)

	const concurrency = 10
	var wg sync.WaitGroup
	errs := make([]error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = m.HandleWebhook(ctx, mockvendor.VendorName, headers, body)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "delivery %d must succeed (idempotent)", i)
	}

	assert.True(t, getBalance(t, db, cash).Equal(decimal.NewFromInt(100_000)),
		"balance must reflect exactly ONE money_in despite %d concurrent deliveries", concurrency)

	var eventCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM payin_webhook_events WHERE vendor = 'mockvendor' AND vendor_event_id = 'evt-dup-1'`,
	).Scan(&eventCount))
	assert.Equal(t, 1, eventCount, "exactly one webhook event row, not one per delivery")

	var txCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM ledger_transactions WHERE idempotency_key = 'payin:mockvendor:evt-dup-1'`,
	).Scan(&txCount))
	assert.Equal(t, 1, txCount, "exactly one ledger transaction, not one per delivery")
}

// TestPayin_HandleWebhook_BadSignature_NoRowWritten_BalanceUnchanged proves
// the receiver-level DoD: a bad signature has zero side effects.
func TestPayin_HandleWebhook_BadSignature_NoRowWritten_BalanceUnchanged(t *testing.T) {
	db := setupPayinTestDB(t)
	m := newPayinModule(db)
	ctx := context.Background()

	userID := uuid.New()
	cash := createUserCashAccount(t, db, userID)

	body := settledWebhookBody("evt-bad-sig", "ref-bad-sig", userID, 50_000)
	headers := http.Header{}
	headers.Set(mockvendor.SignatureHeader, "0000deadbeef")

	err := m.HandleWebhook(ctx, mockvendor.VendorName, headers, body)
	require.Error(t, err)

	assert.True(t, getBalance(t, db, cash).Equal(decimal.Zero))

	var count int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM payin_webhook_events WHERE vendor_event_id = 'evt-bad-sig'`,
	).Scan(&count))
	assert.Equal(t, 0, count, "a bad signature must write nothing")
}

// TestPayin_ReplayEvent_FailedEvent_PostsSuccessfully_ThenRejectsDoublePost
// is docs/plan/22 Task T4's required integration test: a 'failed' webhook
// event can be replayed and money moves exactly once — a second replay of
// the same now-posted event is rejected outright, never a second money_in.
//
// The 'failed' row is seeded directly rather than produced by an organic
// HandleWebhook failure: money_in's only real business validators
// (PositiveAmountValidator, IntegralAmountValidator) can never actually
// fire through this path, because ledger_transactions' own DB CHECK
// constraint (amount > 0) already rejects a bad amount at the very first
// INSERT — before any Go-level Validate() runs at all — and every OTHER
// realistic failure mode (missing/suspended account) is a STRUCTURAL
// failure (rolls back, never commits 'failed') per docs/plan/14's
// validateAccounts design, not a business one. Both were verified
// empirically while writing this test. In practice, then, a 'failed'
// payin_webhook_events row today mainly arises from a hook/processor this
// codebase adds later that genuinely wraps its failure in
// apperror.NewBizErr — this test proves REPLAY's own mechanics
// (fetch -> check status -> re-post -> mark posted, idempotent against a
// second replay) work correctly against a real ledger regardless of how
// the row got into 'failed' in the first place; the classification logic
// itself (a real LedgerError -> 'failed', not 'received') is proven by the
// existing unit test TestHandleWebhook_BusinessFailure_MarkedFailed_NotRetryable.
func TestPayin_ReplayEvent_FailedEvent_PostsSuccessfully_ThenRejectsDoublePost(t *testing.T) {
	db := setupPayinTestDB(t)
	m := newPayinModule(db)
	ctx := context.Background()

	userID := uuid.New()
	cash := createUserCashAccount(t, db, userID)

	id := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO payin_webhook_events
			(id, vendor, vendor_event_id, external_ref, user_id, amount, currency, raw, status, created_at, updated_at)
		VALUES ($1, 'mockvendor', 'evt-replay-1', 'ref-replay-1', $2, 50000, 'IDR', '{}'::jsonb, 'failed', now(), now())`,
		id, userID)
	require.NoError(t, err)

	require.NoError(t, m.ReplayEvent(ctx, id))
	assert.True(t, getBalance(t, db, cash).Equal(decimal.NewFromInt(50_000)))

	var status string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM payin_webhook_events WHERE id = $1`, id).Scan(&status))
	assert.Equal(t, "posted", status)

	// Replaying an already-posted event must be rejected, never a double post.
	err = m.ReplayEvent(ctx, id)
	assert.ErrorIs(t, err, payin.ErrAlreadyPosted)
	assert.True(t, getBalance(t, db, cash).Equal(decimal.NewFromInt(50_000)),
		"a rejected replay must not move money again")
}

// TestPayin_Topup_FullJourney_CreateWebhookSettleIdempotent proves the
// complete docs/plan/25 Task T3 vertical end to end against a real
// Postgres + real ledger: create intent -> signed webhook carrying the
// intent's Reference in external_ref (with a DELIBERATELY DIFFERENT
// payload user_id, proving the vendor genuinely never needs to know the
// real one) -> balance increases -> intent flips to 'settled' -> a
// redelivery of the exact same webhook is a safe no-op (exactly one
// money_in, intent stays settled, balance unchanged).
func TestPayin_Topup_FullJourney_CreateWebhookSettleIdempotent(t *testing.T) {
	db := setupPayinTestDB(t)
	m := newPayinModule(db)
	ctx := context.Background()

	userID := uuid.New()
	cash := createUserCashAccount(t, db, userID)

	intent, err := m.CreateTopupIntent(ctx, userID, decimal.NewFromInt(250_000))
	require.NoError(t, err)
	assert.Equal(t, "pending", intent.Status)
	assert.NotEmpty(t, intent.Reference)

	// The webhook payload's own user_id is a RANDOM, unrelated uuid — the
	// vendor genuinely never learned the real one; only Reference (in
	// external_ref) ties this delivery back to the intent.
	body := settledWebhookBody("evt-topup-e2e-1", intent.Reference, uuid.New(), 250_000)
	require.NoError(t, m.HandleWebhook(ctx, mockvendor.VendorName, signedHeaders(body), body))

	assert.True(t, getBalance(t, db, cash).Equal(decimal.NewFromInt(250_000)),
		"the INTENT's user must be credited, not the payload's unrelated user_id")

	settled, err := m.GetTopupIntent(ctx, intent.ID)
	require.NoError(t, err)
	assert.Equal(t, "settled", settled.Status)
	require.NotNil(t, settled.SettledEventID)

	// Redelivery of the exact same webhook (same vendor_event_id) must be
	// a safe, idempotent no-op.
	require.NoError(t, m.HandleWebhook(ctx, mockvendor.VendorName, signedHeaders(body), body))
	assert.True(t, getBalance(t, db, cash).Equal(decimal.NewFromInt(250_000)),
		"redelivery must not double-credit")

	var txCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM ledger_transactions WHERE idempotency_key = $1`,
		"payin:mockvendor:evt-topup-e2e-1",
	).Scan(&txCount))
	assert.Equal(t, 1, txCount, "exactly one money_in transaction, not one per delivery")

	stillSettled, err := m.GetTopupIntent(ctx, intent.ID)
	require.NoError(t, err)
	assert.Equal(t, "settled", stillSettled.Status)
}
