//go:build integration

// White-box (package payout, not payout_test) so these tests can call
// settle/cancel/hold directly — docs/roadmap/archive/23 Task T4's own core point is
// racing THOSE internal methods against each other and against the
// ledger's K3 guard, not going through the public Create() vertical (that
// vertical is already covered by payout_integration_test.go).
package payout

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/payout/repository"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/internal/vendorgw/mockvendor"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
)

func raceMigrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}

func setupRaceTestDB(t *testing.T) *database.DBSQL {
	t.Helper()
	ctx := context.Background()

	const dbName, dbUser, dbPassword = "seev_test", "test", "secret"

	container, err := postgres.Run(ctx,
		"postgres:16.14-alpine",
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

	require.NoError(t, testutil.ApplyServiceMigrations(raceMigrationsSourceURL(t), dsn))

	cfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	}
	db, err := database.New(ctx, cfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

func raceGetBalance(t *testing.T, db *database.DBSQL, accountID uuid.UUID) decimal.Decimal {
	t.Helper()
	var balance int64
	err := db.QueryRowContext(context.Background(),
		`SELECT balance FROM account_balances WHERE account_id = $1`, accountID).Scan(&balance)
	require.NoError(t, err)
	return decimal.NewFromInt(balance)
}

func raceAssertLedgerBalanced(t *testing.T, db *database.DBSQL) {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT * FROM fn_verify_ledger_balance('-infinity', 'infinity')`)
	require.NoError(t, err)
	defer rows.Close()
	assert.False(t, rows.Next(), "fn_verify_ledger_balance found an unbalanced transaction")
}

// setupHeldRequest provisions a funded user and drives a payout_requests
// row through hold -> submitted (via the unexported hold()/repo calls
// directly, bypassing any actual vendor call) so tests can race
// settle()/cancel() from the SAME starting status submit() itself always
// reaches before ever calling settle/cancel in the real Create() flow —
// TransitionToSettled/TransitionToCancelled's predecessor set is
// ('submitted', 'vendor_pending') only (docs/roadmap/archive/23 Task T1's TOCTOU
// fix), so racing from 'held' directly would be racing a state Create()
// never actually calls settle/cancel from.
func setupHeldRequest(t *testing.T, db *database.DBSQL, m *Module, ledgerModule *testutil.LedgerHarness, amount int64) (model.PayoutRequest, uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	userID := uuid.New()
	require.NoError(t, ledgerModule.ProvisionUser(ctx, userID, "IDR"))
	require.NoError(t, ledgerModule.Post(ctx, ledgerclient.Command{
		IdempotencyKey: "topup-" + userID.String(), Type: "money_in",
		Amount: decimal.NewFromInt(amount), UserID: userID,
		Metadata: map[string]any{"gateway": "bca"},
	}))

	dest, _ := json.Marshal(map[string]string{"bank_code": "014", "account_no": "1234567890"})
	req := model.PayoutRequest{
		ID: generalutil.NewV7(), UserID: userID, Amount: decimal.NewFromInt(amount / 2), Currency: "IDR",
		Vendor: mockvendor.VendorName, Destination: dest, CreatedBy: "test",
	}
	require.NoError(t, m.repo.Insert(ctx, req))
	require.NoError(t, m.hold(ctx, req))
	_, err := m.repo.TransitionToSubmitted(ctx, req.ID)
	require.NoError(t, err)

	accounts, err := ledgerModule.ListAccounts(ctx, userID)
	require.NoError(t, err)
	var cash uuid.UUID
	for _, a := range accounts {
		if a.Type == "cash" {
			cash = a.ID
		}
	}
	require.NotEqual(t, uuid.Nil, cash)

	got, err := m.repo.Get(ctx, req.ID)
	require.NoError(t, err)
	return got, cash
}

func newRaceModule(db *database.DBSQL) (*Module, *testutil.LedgerHarness) {
	ledgerModule := testutil.NewLedgerHarness(db)
	registry := vendorgw.NewRegistry()
	registry.AddPayout(mockvendor.NewPayoutProvider(mockvendor.VendorName))
	m := &Module{
		repo:     repository.NewRepository(db),
		poster:   ledgerModule,
		registry: registry,
		routing:  routeTo(mockvendor.VendorName, "bca"),
		logger:   discardLogger(),
	}
	return m, ledgerModule
}

// TestDoubleCallback_ConcurrentSettle_ExactlyOnePosted is docs/roadmap/archive/23 Task
// T4's "double-callback" required test: two concurrent settle() calls for
// the SAME held request (as if two redelivered vendor callbacks, or a
// callback racing the resume job, both decided the payout settled) must
// result in exactly one withdraw_settle landing — the SAME deterministic
// idempotency key on both calls means the ledger's own idempotency dedup
// (not K3's closed_by_tx_id, which guards DIFFERENT competing keys) is what
// makes the loser's Post a safe no-op rather than a second transfer.
func TestDoubleCallback_ConcurrentSettle_ExactlyOnePosted(t *testing.T) {
	db := setupRaceTestDB(t)
	m, ledgerModule := newRaceModule(db)
	ctx := context.Background()

	req, cash := setupHeldRequest(t, db, m, ledgerModule, 200_000)

	const concurrency = 10
	var wg sync.WaitGroup
	var errCount int64
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.settle(ctx, req.ID, "bca"); err != nil {
				atomic.AddInt64(&errCount, 1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(0), errCount, "every concurrent settle call must succeed (idempotent), none should surface an error")

	final, err := m.repo.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSettled, final.Status)

	var txCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM ledger_transactions WHERE idempotency_key = $1`, settleIdempotencyKey(req.ID),
	).Scan(&txCount))
	assert.Equal(t, 1, txCount, "exactly one withdraw_settle transaction, not one per concurrent caller")

	assert.True(t, raceGetBalance(t, db, cash).Equal(decimal.NewFromInt(100_000)),
		"cash must reflect exactly ONE settle despite %d concurrent callers", concurrency)

	raceAssertLedgerBalanced(t, db)
}

// TestSettleAfterCancel_LedgerRejectsViaK3_ReconciledNoMoneyMoved is
// docs/roadmap/archive/23 Task T4's other required test — this one exercises K3
// directly: cancel closes hold_tx_id first, then a late settle attempt
// (a DIFFERENT idempotency key targeting the SAME hold_tx_id) must be
// rejected by the ledger's atomic closed_by_tx_id guard
// (ledgererr.ErrAlreadyClosed), and payout must reconcile that into
// error_message rather than surfacing it as a caller-visible failure or
// moving any money.
func TestSettleAfterCancel_LedgerRejectsViaK3_ReconciledNoMoneyMoved(t *testing.T) {
	db := setupRaceTestDB(t)
	m, ledgerModule := newRaceModule(db)
	ctx := context.Background()

	req, cash := setupHeldRequest(t, db, m, ledgerModule, 200_000)

	require.NoError(t, m.cancel(ctx, req.ID, "bca", "admin cancel"))

	afterCancel, err := m.repo.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCancelled, afterCancel.Status)
	assert.True(t, raceGetBalance(t, db, cash).Equal(decimal.NewFromInt(200_000)), "cancel must return the full held amount")

	// Late settle callback arrives after the cancel already closed hold_tx_id.
	err = m.settle(ctx, req.ID, "bca")
	assert.NoError(t, err, "a lost K3 race must be reconciled, never surfaced as a caller-visible error")

	final, err := m.repo.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCancelled, final.Status, "status must remain cancelled — the late settle must NOT overwrite it")
	assert.True(t, strings.Contains(final.ErrorMessage, "lost race"), "the conflict must be recorded in error_message, got %q", final.ErrorMessage)

	assert.True(t, raceGetBalance(t, db, cash).Equal(decimal.NewFromInt(200_000)), "no money may move on the rejected late settle")

	// The idempotency-gate step inserts a header row for audit purposes
	// even on a rejected attempt (docs/development/project-guide.md's execTransfer ordering rule:
	// failed validations must stay auditable, not silently vanish) — so a
	// row existing is expected; what must NEVER happen is that row reaching
	// 'posted' status, which is what would mean money actually moved.
	var settleTxStatus string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT status FROM ledger_transactions WHERE idempotency_key = $1`, settleIdempotencyKey(req.ID),
	).Scan(&settleTxStatus))
	assert.NotEqual(t, "posted", settleTxStatus, "the rejected settle must never reach 'posted' status")

	raceAssertLedgerBalanced(t, db)
}
