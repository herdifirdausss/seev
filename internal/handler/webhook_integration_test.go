//go:build integration

// Proves the payin webhook route (docs/roadmap/archive/22 Task T3) end to end through
// the REAL public router — not just internal/payin.Module in isolation
// (that's covered by internal/payin's own integration tests) — specifically
// the HTTP-layer concerns: bypasses JWT/CORS, enforces the body cap, and
// maps internal/payin's error contract to the right status codes.
package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/payin"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/internal/vendorgw/mockvendor"
	"github.com/herdifirdausss/seev/pkg/database"
)

func webhookMigrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}

func setupWebhookTestDB(t *testing.T) *database.DBSQL {
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

	require.NoError(t, testutil.ApplyServiceMigrations(webhookMigrationsSourceURL(t), dsn))

	cfg := config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	}
	db, err := database.New(ctx, cfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

func createWebhookTestUser(t *testing.T, db *database.DBSQL, userID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	accountID := uuid.New()

	_, err := db.ExecContext(ctx, `
		INSERT INTO accounts (id, owner_id, owner_type, type, currency, status, created_by)
		VALUES ($1, $2, 'user', 'cash', 'IDR', 'active', 'webhook_integration_test')`,
		accountID, userID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO account_balances (account_id) VALUES ($1)`, accountID)
	require.NoError(t, err)
	return accountID
}

func webhookTestBalance(t *testing.T, db *database.DBSQL, accountID uuid.UUID) decimal.Decimal {
	t.Helper()
	var balance int64
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT balance FROM account_balances WHERE account_id = $1`, accountID).Scan(&balance))
	return decimal.NewFromInt(balance)
}

const webhookTestMockSecret = "webhook-test-secret"

func webhookTestConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{
			Env:               "development",
			BaseURL:           "http://localhost:8080",
			RateLimitRequests: 10,
			RateLimitPer:      time.Minute,
			RateLimitBurst:    10,
		},
		JWT: config.JWTConfig{
			Secret:       "supersecretkeythatisatleast32chars!",
			Issuer:       "seev-integration",
			AccessExpiry: 15 * time.Minute,
		},
	}
}

func newWebhookTestRouter(t *testing.T, db *database.DBSQL) http.Handler {
	t.Helper()
	ledgerClient := testutil.NewLedgerHarness(db)

	registry := vendorgw.NewRegistry()
	registry.AddPayin(mockvendor.New(mockvendor.VendorName, webhookTestMockSecret))
	payinModule := payin.NewModule(db, ledgerClient, registry, 0, nil, nil, nil)
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	payinModule.RegisterGRPC(grpcServer)
	go func() { _ = grpcServer.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(); grpcServer.Stop(); _ = listener.Close() })

	deps := &Dependencies{DB: db, Payin: payinv1.NewPayinServiceClient(conn)}
	return NewRouter(webhookTestConfig(), deps, slog.Default())
}

func settledBody(eventID string, userID uuid.UUID, amount int64) []byte {
	return []byte(`{"event_id":"` + eventID + `","external_ref":"ref-` + eventID +
		`","user_id":"` + userID.String() + `","amount":"` + fmt.Sprint(amount) +
		`","currency":"IDR","occurred_at":"2026-07-13T00:00:00Z","type":"payment.settled"}`)
}

func TestWebhookRoute_ValidSignature_200_MovesMoney_ThroughFullRouter(t *testing.T) {
	db := setupWebhookTestDB(t)
	router := newWebhookTestRouter(t, db)

	userID := uuid.New()
	cash := createWebhookTestUser(t, db, userID)

	body := settledBody("evt-http-1", userID, 75_000)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/mockvendor", strings.NewReader(string(body)))
	req.Header.Set(mockvendor.SignatureHeader, mockvendor.Sign(webhookTestMockSecret, body))
	// No Authorization header at all — this route must not require one.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, webhookTestBalance(t, db, cash).Equal(decimal.NewFromInt(75_000)))
}

func TestWebhookRoute_UnknownVendor_404(t *testing.T) {
	db := setupWebhookTestDB(t)
	router := newWebhookTestRouter(t, db)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/nosuchvendor", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestWebhookRoute_BadSignature_401_NoBalanceChange(t *testing.T) {
	db := setupWebhookTestDB(t)
	router := newWebhookTestRouter(t, db)

	userID := uuid.New()
	cash := createWebhookTestUser(t, db, userID)

	body := settledBody("evt-http-bad-sig", userID, 10_000)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/mockvendor", strings.NewReader(string(body)))
	req.Header.Set(mockvendor.SignatureHeader, "0000deadbeef")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.True(t, webhookTestBalance(t, db, cash).Equal(decimal.Zero))
}

// NOTE: an over-cap-body -> 413 test through the FULL router (as opposed to
// webhookHandler in isolation, see webhook_test.go) is not reliable right
// now: pkg/middleware.WithLogger's request-body logging (pkg/logger's
// readBody, capped at 16KiB) silently truncates any body over 16KiB before
// this handler's own 64KiB http.MaxBytesReader ever sees it — a
// pre-existing bug unrelated to payin, flagged separately for a dedicated
// fix (see task tracker). Testing the 413 contract through the full stack
// here would either flake once that bug is fixed or bake the bug's
// behavior into this suite as if it were intentional. The isolated
// TestWebhookHandler_BodyOverCap_413 (webhook_test.go) proves this
// handler's own cap logic is correct independent of that middleware issue.

// TestWebhookRoute_NoVendorConfigured_404 proves the DoD default-safe
// behavior (docs/roadmap/archive/22 Task T3): with deps.Payin nil (no vendor ever
// wired), every /webhooks/* request 404s — byte-identical to before this
// feature existed.
func TestWebhookRoute_NoVendorConfigured_404(t *testing.T) {
	deps := &Dependencies{} // Payin left nil
	router := NewRouter(webhookTestConfig(), deps, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/webhooks/mockvendor", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestWebhookRoute_DoesNotSetCORSHeaders proves the webhook route was
// mounted OUTSIDE the CORS-bearing `global` chain (docs/roadmap/archive/22 Task T3
// DoD) — a request carrying an Origin header must NOT get an
// Access-Control-Allow-Origin response header the way a normal /api/v1/...
// route would.
func TestWebhookRoute_DoesNotSetCORSHeaders(t *testing.T) {
	db := setupWebhookTestDB(t)
	router := newWebhookTestRouter(t, db)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/nosuchvendor", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"),
		"the webhook route must not go through WithCORS")
}
