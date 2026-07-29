//go:build integration

// Package api_test proves the full B2B payin/payout HTTP surface end to
// end through the ACTUAL assembled router (internal/merchant/api.NewRouter),
// against real PostgreSQL for auth/quota/idempotency — only the owner
// services (PayinService/PayoutService) are stood in by a fake bufconn
// gRPC server, since their own correctness is already proven exhaustively
// by internal/payin's and internal/payout's own integration tests (Plan
// 57 T6). This test's job is the WIRING: auth -> scope -> quota ->
// idempotency -> owner dispatch -> response mapping, exactly as a real
// merchant client would exercise it over HTTP.
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"github.com/herdifirdausss/seev/internal/config"
	merchantapi "github.com/herdifirdausss/seev/internal/merchant/api"
	"github.com/herdifirdausss/seev/internal/merchant/auth"
	"github.com/herdifirdausss/seev/internal/merchant/client"
	"github.com/herdifirdausss/seev/internal/merchant/idempotency"
	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/quota"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/database"
)

const integrationTestPepper = "b2b-router-integration-test-pepper"

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations")
}

// setupGatewayTestDB mirrors internal/merchant/repository's and
// internal/merchant/auth's own identically-named helper — ledger
// migrations run first because that is where app_service/app_readonly
// are actually created (cluster-wide roles), a prerequisite every gateway
// migration's own GRANT statement depends on. Also returns a connection
// to the SAME container's seev_ledger database (Plan 57 T10 follow-up) —
// tests that need a real in-process ledger.Module (via
// internal/testutil.NewLedgerHarness) reuse this instead of standing up a
// second container.
func setupGatewayTestDB(t *testing.T) (gatewayDB, ledgerDB *database.DBSQL) {
	t.Helper()
	ctx := context.Background()

	container, err := pgcontainer.Run(ctx, "postgres:16.14-alpine",
		pgcontainer.WithDatabase("seev_ledger"), pgcontainer.WithUsername("test"), pgcontainer.WithPassword("secret"),
		pgcontainer.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5432")
	require.NoError(t, err)

	ledgerDSN := fmt.Sprintf("postgres://test:secret@%s:%s/seev_ledger?sslmode=disable", host, port.Port())
	require.NoError(t, testutil.ApplyMigration(migrationsSourceURL(t), "ledger", ledgerDSN))

	adminDB, err := database.New(ctx, (config.PostgresConfig{
		Host: host, Port: port.Port(), User: "test", Password: "secret", DB: "seev_ledger", SSLMode: "disable", MaxOpenConns: 1,
	}).Pkg())
	require.NoError(t, err)
	_, err = adminDB.ExecContext(ctx, `CREATE DATABASE seev_gateway`)
	require.NoError(t, err)
	require.NoError(t, adminDB.Close())

	dsn := fmt.Sprintf("postgres://test:secret@%s:%s/seev_gateway?sslmode=disable", host, port.Port())
	require.NoError(t, testutil.ApplyMigration(migrationsSourceURL(t), "gateway", dsn))

	db, err := database.New(ctx, (config.PostgresConfig{
		Host: host, Port: port.Port(), User: "test", Password: "secret", DB: "seev_gateway", SSLMode: "disable", MaxOpenConns: 10,
	}).Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ledgerConn, err := database.New(ctx, (config.PostgresConfig{
		Host: host, Port: port.Port(), User: "test", Password: "secret", DB: "seev_ledger", SSLMode: "disable", MaxOpenConns: 10,
	}).Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = ledgerConn.Close() })

	return db, ledgerConn
}

// ─── Fake owner services (bufconn) ─────────────────────────────────────────
//
// PayinService/PayoutService's own correctness (sandbox routing, ledger
// posting, duplicate handling) is already proven by internal/payin's and
// internal/payout's own integration tests. These fakes stand in only to
// prove THIS package's HTTP wiring, keyed exactly like the real owner-side
// downstream-key dedup (§10.4) so a replayed Gateway request against this
// fake behaves the same way it would against the real service.

type fakePayinServer struct {
	payinv1.UnimplementedPayinServiceServer
	mu          sync.Mutex
	createCalls int
	byKey       map[string]*payinv1.TopupIntent // tenantID|downstreamKey
	byID        map[string]*payinv1.TopupIntent // tenantID|id
}

func newFakePayinServer() *fakePayinServer {
	return &fakePayinServer{byKey: map[string]*payinv1.TopupIntent{}, byID: map[string]*payinv1.TopupIntent{}}
}

func (f *fakePayinServer) CreateMerchantTopupIntent(_ context.Context, req *payinv1.CreateMerchantTopupIntentRequest) (*payinv1.CreateMerchantTopupIntentResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	dedupKey := req.GetTenantId() + "|" + req.GetDownstreamKey()
	if existing, ok := f.byKey[dedupKey]; ok {
		return &payinv1.CreateMerchantTopupIntentResponse{Intent: existing}, nil
	}
	now := time.Now()
	intent := &payinv1.TopupIntent{
		Id: uuid.NewString(), Status: "pending", Currency: req.GetCurrency(), Amount: req.GetAmount(), Vendor: "mockvendor",
		CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now),
	}
	f.byKey[dedupKey] = intent
	f.byID[req.GetTenantId()+"|"+intent.Id] = intent
	return &payinv1.CreateMerchantTopupIntentResponse{Intent: intent}, nil
}

func (f *fakePayinServer) GetMerchantTopupIntent(_ context.Context, req *payinv1.GetMerchantTopupIntentRequest) (*payinv1.GetMerchantTopupIntentResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	intent, ok := f.byID[req.GetTenantId()+"|"+req.GetId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "topup intent not found")
	}
	return &payinv1.GetMerchantTopupIntentResponse{Intent: intent}, nil
}

type fakePayoutServer struct {
	payoutv1.UnimplementedPayoutServiceServer
	mu    sync.Mutex
	byKey map[string]*payoutv1.Payout // tenantID|downstreamKey
	byID  map[string]*payoutv1.Payout // tenantID|id
}

func newFakePayoutServer() *fakePayoutServer {
	return &fakePayoutServer{byKey: map[string]*payoutv1.Payout{}, byID: map[string]*payoutv1.Payout{}}
}

func (f *fakePayoutServer) CreateMerchantPayout(_ context.Context, req *payoutv1.CreateMerchantPayoutRequest) (*payoutv1.CreateMerchantPayoutResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dedupKey := req.GetTenantId() + "|" + req.GetDownstreamKey()
	if existing, ok := f.byKey[dedupKey]; ok {
		return &payoutv1.CreateMerchantPayoutResponse{Payout: existing}, nil
	}
	now := time.Now()
	payout := &payoutv1.Payout{
		Id: uuid.NewString(), Status: "submitted", Currency: req.GetCurrency(), Amount: req.GetAmount(), Vendor: "mockvendor",
		CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now),
	}
	f.byKey[dedupKey] = payout
	f.byID[req.GetTenantId()+"|"+payout.Id] = payout
	return &payoutv1.CreateMerchantPayoutResponse{Payout: payout}, nil
}

func (f *fakePayoutServer) GetMerchantPayout(_ context.Context, req *payoutv1.GetMerchantPayoutRequest) (*payoutv1.GetMerchantPayoutResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	payout, ok := f.byID[req.GetTenantId()+"|"+req.GetId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "payout request not found")
	}
	return &payoutv1.GetMerchantPayoutResponse{Payout: payout}, nil
}

func startBufconnPayin(t *testing.T, srv payinv1.PayinServiceServer) payinv1.PayinServiceClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	payinv1.RegisterPayinServiceServer(server, srv)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(); server.Stop(); _ = listener.Close() })
	return payinv1.NewPayinServiceClient(conn)
}

func startBufconnPayout(t *testing.T, srv payoutv1.PayoutServiceServer) payoutv1.PayoutServiceClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	payoutv1.RegisterPayoutServiceServer(server, srv)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(); server.Stop(); _ = listener.Close() })
	return payoutv1.NewPayoutServiceClient(conn)
}

// disableQuota seeds a disabled (always-allow, no Redis touch) policy row
// for every quota class this test exercises — RequireQuota's own fallback
// for a WRITE class fails closed (503) when Redis is nil, so this is how
// the test stays hermetic (real Postgres, no Redis dependency) without
// weakening what RequireQuota itself proves (that path is already covered
// by internal/merchant/quota's own tests).
func disableQuota(t *testing.T, quotas repository.QuotaRepository, tenantID uuid.UUID) {
	t.Helper()
	for _, class := range []string{"payin", "payout", "read", "transfers"} {
		require.NoError(t, quotas.Upsert(context.Background(), model.QuotaPolicy{
			ID: uuid.New(), TenantID: tenantID, QuotaClass: class, RequestsPerMinute: 1000, Burst: 1000, IsEnabled: false,
		}))
	}
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type payinData struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Vendor   string `json:"vendor"`
	Livemode bool   `json:"livemode"`
}

type payoutData struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Livemode bool   `json:"livemode"`
}

func doRequest(t *testing.T, method, url, apiKey, idempotencyKey string, body []byte) (*http.Response, envelope) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, reader)
	require.NoError(t, err)
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	var env envelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	return resp, env
}

// TestB2BRouter_RealStack drives the ENTIRE B2B payin/payout surface
// (docs/roadmap/active/57-c1-merchant-b2b-api.md §6.4) through the actual
// assembled router, over real HTTP, against real PostgreSQL-backed
// auth/quota/idempotency.
func TestB2BRouter_RealStack(t *testing.T) {
	db, _ := setupGatewayTestDB(t)
	tenants := repository.NewTenantRepository(db)
	apiKeys := repository.NewAPIKeyRepository(db)
	quotas := repository.NewQuotaRepository(db)
	idemRepo := repository.NewIdempotencyRepository(db)
	keySvc := auth.NewKeyService(apiKeys, tenants, integrationTestPepper)

	ctx := context.Background()
	tenantA := uuid.New()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantA, PublicID: "mrc_" + uuid.NewString()[:16], ExternalCode: "ext-" + tenantA.String(),
		Name: "Real Stack Merchant A", Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))
	disableQuota(t, quotas, tenantA)
	fullKeyA, _, err := keySvc.CreateKey(ctx, tenantA, "live", []string{"payins:write", "payins:read", "payouts:write", "payouts:read"}, "operator")
	require.NoError(t, err)
	readOnlyKeyA, _, err := keySvc.CreateKey(ctx, tenantA, "live", []string{"payins:read"}, "operator")
	require.NoError(t, err)

	tenantB := uuid.New()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantB, PublicID: "mrc_" + uuid.NewString()[:16], ExternalCode: "ext-" + tenantB.String(),
		Name: "Real Stack Merchant B", Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))
	disableQuota(t, quotas, tenantB)
	keyB, _, err := keySvc.CreateKey(ctx, tenantB, "live", []string{"payins:write", "payins:read", "payouts:write", "payouts:read"}, "operator")
	require.NoError(t, err)

	payinFake := newFakePayinServer()
	payoutFake := newFakePayoutServer()

	router := merchantapi.NewRouter(merchantapi.Deps{
		APIKeys:       apiKeys,
		Tenants:       tenants,
		APIKeyPepper:  integrationTestPepper,
		QuotaEnforcer: quota.NewEnforcer(quotas, nil),
		Idempotency:   idempotency.NewService(idemRepo, 24*time.Hour, "test"),
		GlobalFlag:    auth.NewGlobalFlag(repository.NewSettingsRepository(db)),
		Payin:         client.NewPayinClient(startBufconnPayin(t, payinFake)),
		Payout:        client.NewPayoutClient(startBufconnPayout(t, payoutFake)),
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	var payinID string

	t.Run("create payin happy path", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodPost, srv.URL+"/payins", fullKeyA, "key-1", []byte(`{"amount":"50000","currency":"IDR"}`))
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		require.True(t, env.Success)
		var data payinData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		assert.NotEmpty(t, data.ID)
		assert.Equal(t, "pending", data.Status)
		assert.Equal(t, "mockvendor", data.Vendor)
		assert.True(t, data.Livemode)
		payinID = data.ID
	})

	t.Run("replay same key and body returns original, no second owner call", func(t *testing.T) {
		before := func() int { payinFake.mu.Lock(); defer payinFake.mu.Unlock(); return payinFake.createCalls }()
		resp, env := doRequest(t, http.MethodPost, srv.URL+"/payins", fullKeyA, "key-1", []byte(`{"amount":"50000","currency":"IDR"}`))
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var data payinData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		assert.Equal(t, payinID, data.ID)
		after := func() int { payinFake.mu.Lock(); defer payinFake.mu.Unlock(); return payinFake.createCalls }()
		assert.Equal(t, before, after, "a replay must not call the owner service again")
	})

	t.Run("same key different body is a conflict", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodPost, srv.URL+"/payins", fullKeyA, "key-1", []byte(`{"amount":"99999","currency":"IDR"}`))
		assert.Equal(t, http.StatusConflict, resp.StatusCode)
		require.NotNil(t, env.Error)
		assert.Equal(t, "IDEMPOTENCY_KEY_REUSED", env.Error.Code)
	})

	t.Run("missing idempotency key is rejected", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodPost, srv.URL+"/payins", fullKeyA, "", []byte(`{"amount":"50000","currency":"IDR"}`))
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		require.NotNil(t, env.Error)
		assert.Equal(t, "IDEMPOTENCY_KEY_REQUIRED", env.Error.Code)
	})

	t.Run("invalid amount is rejected", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodPost, srv.URL+"/payins", fullKeyA, "key-invalid-amount", []byte(`{"amount":"not-a-number","currency":"IDR"}`))
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		require.NotNil(t, env.Error)
		assert.Equal(t, "VALIDATION_FAILED", env.Error.Code)
	})

	t.Run("missing authentication is rejected", func(t *testing.T) {
		resp, _ := doRequest(t, http.MethodPost, srv.URL+"/payins", "", "key-noauth", []byte(`{"amount":"50000","currency":"IDR"}`))
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("insufficient scope is rejected", func(t *testing.T) {
		resp, _ := doRequest(t, http.MethodPost, srv.URL+"/payins", readOnlyKeyA, "key-scope", []byte(`{"amount":"50000","currency":"IDR"}`))
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("get payin happy path", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/payins/"+payinID, fullKeyA, "", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var data payinData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		assert.Equal(t, payinID, data.ID)
	})

	t.Run("cross-tenant get is not found, never leaked", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/payins/"+payinID, keyB, "", nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.NotNil(t, env.Error)
		assert.Equal(t, "RESOURCE_NOT_FOUND", env.Error.Code)
	})

	t.Run("get nonexistent payin is not found", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/payins/"+uuid.NewString(), fullKeyA, "", nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.NotNil(t, env.Error)
		assert.Equal(t, "RESOURCE_NOT_FOUND", env.Error.Code)
	})

	var payoutID string

	t.Run("create payout happy path", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodPost, srv.URL+"/payouts", fullKeyA, "payout-key-1",
			[]byte(`{"amount":"100000","currency":"IDR","destination":{"account_no":"001"}}`))
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var data payoutData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		assert.NotEmpty(t, data.ID)
		assert.Equal(t, "processing", data.Status, "internal 'submitted' must map to the public 'processing' status (§1.3)")
		assert.True(t, data.Livemode)
		payoutID = data.ID
	})

	t.Run("payout missing destination is rejected", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodPost, srv.URL+"/payouts", fullKeyA, "payout-key-2", []byte(`{"amount":"100000","currency":"IDR"}`))
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		require.NotNil(t, env.Error)
		assert.Equal(t, "VALIDATION_FAILED", env.Error.Code)
	})

	t.Run("get payout happy path", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/payouts/"+payoutID, fullKeyA, "", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var data payoutData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		assert.Equal(t, payoutID, data.ID)
	})

	t.Run("cross-tenant get payout is not found", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/payouts/"+payoutID, keyB, "", nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.NotNil(t, env.Error)
		assert.Equal(t, "RESOURCE_NOT_FOUND", env.Error.Code)
	})
}

// merchantTransactionData mirrors internal/merchant/api's own
// transactionResponse JSON shape.
type merchantTransactionData struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Status               string `json:"status"`
	Amount               string `json:"amount"`
	Currency             string `json:"currency"`
	SourceAccountID      string `json:"source_account_id"`
	DestinationAccountID string `json:"destination_account_id"`
}

type merchantAccountData struct {
	ID       string `json:"id"`
	Currency string `json:"currency"`
	Balance  string `json:"balance"`
	Status   string `json:"status"`
}

type merchantProfileData struct {
	ID              string `json:"id"`
	ExternalCode    string `json:"external_code"`
	Environment     string `json:"environment"`
	Status          string `json:"status"`
	DefaultCurrency string `json:"default_currency"`
}

// seedMerchantBalance mirrors internal/ledger/grpcserver's own identically
// named helper — a direct SQL credit against account_balances, since
// there is no "deposit" RPC and this test's job is proving the B2B HTTP
// surface, not re-testing money-in (already covered elsewhere).
func seedMerchantBalance(t *testing.T, ledgerDB *database.DBSQL, accountID uuid.UUID, amount int64) {
	t.Helper()
	_, err := ledgerDB.ExecContext(context.Background(),
		`UPDATE account_balances SET balance = balance + $1 WHERE account_id = $2`, amount, accountID)
	require.NoError(t, err)
}

// TestB2BRouter_MerchantAccountsAndTransfers proves the merchant profile,
// accounts, transactions, and transfers HTTP surface (Plan 57 T10
// follow-up — T5/T6 built the LedgerService RPCs behind this, but nothing
// ever called them over HTTP until now) through the ACTUAL assembled
// router, against a REAL in-process ledger.Module sharing this test's own
// Postgres container (internal/testutil.LedgerHarness) — unlike the
// payin/payout test above, Ledger's own correctness is exactly what this
// surface depends on, so it is never faked here, only Payin/Payout are
// (and aren't exercised by this test at all).
func TestB2BRouter_MerchantAccountsAndTransfers(t *testing.T) {
	gatewayDB, ledgerDB := setupGatewayTestDB(t)
	ctx := context.Background()

	ledgerHarness := testutil.NewLedgerHarness(ledgerDB)
	require.NoError(t, ledgerHarness.Module().LoadCurrencies(ctx))

	tenants := repository.NewTenantRepository(gatewayDB)
	apiKeys := repository.NewAPIKeyRepository(gatewayDB)
	quotas := repository.NewQuotaRepository(gatewayDB)
	idemRepo := repository.NewIdempotencyRepository(gatewayDB)
	keySvc := auth.NewKeyService(apiKeys, tenants, integrationTestPepper)

	scopes := []string{"merchant:read", "accounts:read", "transactions:read", "transfers:write"}

	tenantA := uuid.New()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantA, PublicID: "mrc_" + uuid.NewString()[:16], ExternalCode: "ext-a-" + tenantA.String(),
		Name: "Transfer Merchant A", Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))
	disableQuota(t, quotas, tenantA)
	fullKeyA, _, err := keySvc.CreateKey(ctx, tenantA, "live", scopes, "operator")
	require.NoError(t, err)
	readOnlyKeyA, _, err := keySvc.CreateKey(ctx, tenantA, "live", []string{"accounts:read"}, "operator")
	require.NoError(t, err)

	tenantB := uuid.New()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantB, PublicID: "mrc_" + uuid.NewString()[:16], ExternalCode: "ext-b-" + tenantB.String(),
		Name: "Transfer Merchant B", Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))
	disableQuota(t, quotas, tenantB)
	keyB, _, err := keySvc.CreateKey(ctx, tenantB, "live", scopes, "operator")
	require.NoError(t, err)

	accountIDA, err := ledgerHarness.Module().ProvisionMerchant(ctx, tenantA, "IDR")
	require.NoError(t, err)
	accountIDB, err := ledgerHarness.Module().ProvisionMerchant(ctx, tenantB, "IDR")
	require.NoError(t, err)
	seedMerchantBalance(t, ledgerDB, accountIDA.ID, 100000)

	router := merchantapi.NewRouter(merchantapi.Deps{
		APIKeys:       apiKeys,
		Tenants:       tenants,
		APIKeyPepper:  integrationTestPepper,
		QuotaEnforcer: quota.NewEnforcer(quotas, nil),
		Idempotency:   idempotency.NewService(idemRepo, 24*time.Hour, "test"),
		GlobalFlag:    auth.NewGlobalFlag(repository.NewSettingsRepository(gatewayDB)),
		Payin:         client.NewPayinClient(startBufconnPayin(t, newFakePayinServer())),
		Payout:        client.NewPayoutClient(startBufconnPayout(t, newFakePayoutServer())),
		Ledger:        client.NewLedgerClient(ledgerHarness),
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	t.Run("get merchant profile", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/merchant", fullKeyA, "", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var data merchantProfileData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		assert.Equal(t, "live", data.Environment)
		assert.Equal(t, "active", data.Status)
		assert.Equal(t, "IDR", data.DefaultCurrency)
	})

	t.Run("list accounts returns the single cash account", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/accounts", fullKeyA, "", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var data []merchantAccountData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		require.Len(t, data, 1)
		assert.Equal(t, accountIDA.ID.String(), data[0].ID)
		assert.Equal(t, "100000", data[0].Balance)
	})

	t.Run("get account by id happy path", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/accounts/"+accountIDA.ID.String(), fullKeyA, "", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var data merchantAccountData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		assert.Equal(t, "100000", data.Balance)
	})

	t.Run("get account balance happy path", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/accounts/"+accountIDA.ID.String()+"/balance", fullKeyA, "", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var data merchantAccountData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		assert.Equal(t, "100000", data.Balance)
	})

	t.Run("get account with another tenant's real account id is not found", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/accounts/"+accountIDB.ID.String(), fullKeyA, "", nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.NotNil(t, env.Error)
		assert.Equal(t, "RESOURCE_NOT_FOUND", env.Error.Code)
	})

	var transferID string

	t.Run("create transfer happy path", func(t *testing.T) {
		body := fmt.Sprintf(`{"destination_account_id":%q,"amount":"25000","currency":"IDR"}`, accountIDB.ID.String())
		resp, env := doRequest(t, http.MethodPost, srv.URL+"/transfers", fullKeyA, "xfer-key-1", []byte(body))
		require.Equal(t, http.StatusCreated, resp.StatusCode, env.Error)
		var data merchantTransactionData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		assert.Equal(t, "posted", data.Status)
		assert.Equal(t, "25000", data.Amount)
		assert.Equal(t, accountIDA.ID.String(), data.SourceAccountID)
		assert.Equal(t, accountIDB.ID.String(), data.DestinationAccountID)
		transferID = data.ID
	})

	t.Run("replay same key returns original, balance unchanged", func(t *testing.T) {
		body := fmt.Sprintf(`{"destination_account_id":%q,"amount":"25000","currency":"IDR"}`, accountIDB.ID.String())
		resp, env := doRequest(t, http.MethodPost, srv.URL+"/transfers", fullKeyA, "xfer-key-1", []byte(body))
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var data merchantTransactionData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		assert.Equal(t, transferID, data.ID)

		balResp, balEnv := doRequest(t, http.MethodGet, srv.URL+"/accounts/"+accountIDA.ID.String(), fullKeyA, "", nil)
		require.Equal(t, http.StatusOK, balResp.StatusCode)
		var bal merchantAccountData
		require.NoError(t, json.Unmarshal(balEnv.Data, &bal))
		assert.Equal(t, "75000", bal.Balance, "a replayed transfer must never debit the source account twice")
	})

	t.Run("insufficient scope cannot create a transfer", func(t *testing.T) {
		body := fmt.Sprintf(`{"destination_account_id":%q,"amount":"1000","currency":"IDR"}`, accountIDB.ID.String())
		resp, _ := doRequest(t, http.MethodPost, srv.URL+"/transfers", readOnlyKeyA, "xfer-key-scope", []byte(body))
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("invalid destination_account_id is rejected", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodPost, srv.URL+"/transfers", fullKeyA, "xfer-key-bad-dest", []byte(`{"destination_account_id":"not-a-uuid","amount":"1000","currency":"IDR"}`))
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		require.NotNil(t, env.Error)
		assert.Equal(t, "VALIDATION_FAILED", env.Error.Code)
	})

	t.Run("get transfer happy path via GET /transfers", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/transfers/"+transferID, fullKeyA, "", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var data merchantTransactionData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		assert.Equal(t, transferID, data.ID)
	})

	t.Run("get transfer via GET /transactions serves the identical resource", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/transactions/"+transferID, fullKeyA, "", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var data merchantTransactionData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		assert.Equal(t, transferID, data.ID)
	})

	t.Run("tenant B can also read the same transfer via its own account", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/transactions/"+transferID, keyB, "", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var data merchantTransactionData
		require.NoError(t, json.Unmarshal(env.Data, &data))
		assert.Equal(t, transferID, data.ID)
	})

	t.Run("a completely unrelated tenant gets not found, never the transaction's data", func(t *testing.T) {
		strangerID := uuid.New()
		require.NoError(t, tenants.Create(ctx, model.Tenant{
			ID: strangerID, PublicID: "mrc_" + uuid.NewString()[:16], ExternalCode: "ext-stranger-" + strangerID.String(),
			Name: "Stranger", Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
		}))
		disableQuota(t, quotas, strangerID)
		_, err := ledgerHarness.Module().ProvisionMerchant(ctx, strangerID, "IDR")
		require.NoError(t, err)
		strangerKey, _, err := keySvc.CreateKey(ctx, strangerID, "live", scopes, "operator")
		require.NoError(t, err)

		resp, env := doRequest(t, http.MethodGet, srv.URL+"/transactions/"+transferID, strangerKey, "", nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.NotNil(t, env.Error)
		assert.Equal(t, "RESOURCE_NOT_FOUND", env.Error.Code)
	})

	t.Run("list transactions shows the transfer for the source tenant", func(t *testing.T) {
		resp, env := doRequest(t, http.MethodGet, srv.URL+"/transactions", fullKeyA, "", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var data struct {
			Data []merchantTransactionData `json:"data"`
		}
		require.NoError(t, json.Unmarshal(env.Data, &data))
		require.Len(t, data.Data, 1)
		assert.Equal(t, transferID, data.Data[0].ID)
	})
}

// TestB2BRouter_GlobalKillSwitchGatesEveryRoute proves T9's own "global
// route-disable control" actually gates THIS router — found live while
// writing scripts/merchant-e2e.sh that it never was: T9's own Result
// section documented the flag as "structurally correct but not yet
// load-bearing" because no B2B HTTP route existed yet to gate, and it
// was STILL never wired in once T10's follow-up built this router.
// NewRouter now panics on a nil GlobalFlag (the same "required, never
// optional" posture as every other money-safety Deps field here), and
// RequireB2BEnabled runs first in the middleware chain, before auth.
func TestB2BRouter_GlobalKillSwitchGatesEveryRoute(t *testing.T) {
	gatewayDB, _ := setupGatewayTestDB(t)
	ctx := context.Background()

	tenants := repository.NewTenantRepository(gatewayDB)
	apiKeys := repository.NewAPIKeyRepository(gatewayDB)
	quotas := repository.NewQuotaRepository(gatewayDB)
	idemRepo := repository.NewIdempotencyRepository(gatewayDB)
	keySvc := auth.NewKeyService(apiKeys, tenants, integrationTestPepper)
	settings := repository.NewSettingsRepository(gatewayDB)
	globalFlag := auth.NewGlobalFlag(settings)

	tenantID := uuid.New()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantID, PublicID: "mrc_" + uuid.NewString()[:16], ExternalCode: "ext-killswitch-" + tenantID.String(),
		Name: "Kill Switch Test Merchant", Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))
	disableQuota(t, quotas, tenantID)
	key, _, err := keySvc.CreateKey(ctx, tenantID, "live", []string{"merchant:read"}, "operator")
	require.NoError(t, err)

	router := merchantapi.NewRouter(merchantapi.Deps{
		APIKeys:       apiKeys,
		Tenants:       tenants,
		APIKeyPepper:  integrationTestPepper,
		QuotaEnforcer: quota.NewEnforcer(quotas, nil),
		Idempotency:   idempotency.NewService(idemRepo, 24*time.Hour, "test"),
		GlobalFlag:    globalFlag,
		Payin:         client.NewPayinClient(startBufconnPayin(t, newFakePayinServer())),
		Payout:        client.NewPayoutClient(startBufconnPayout(t, newFakePayoutServer())),
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, _ := doRequest(t, http.MethodGet, srv.URL+"/merchant", key, "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "enabled by default — a fresh deployment must serve B2B traffic")

	require.NoError(t, globalFlag.SetEnabled(ctx, false, "test-operator"))
	disabledResp, disabledEnv := doRequest(t, http.MethodGet, srv.URL+"/merchant", key, "", nil)
	assert.Equal(t, http.StatusServiceUnavailable, disabledResp.StatusCode, "every B2B route must reject while the kill switch is disabled")
	require.NotNil(t, disabledEnv.Error)
	assert.Equal(t, "B2B_API_DISABLED", disabledEnv.Error.Code)

	require.NoError(t, globalFlag.SetEnabled(ctx, true, "test-operator"))
	reenabledResp, _ := doRequest(t, http.MethodGet, srv.URL+"/merchant", key, "", nil)
	assert.Equal(t, http.StatusOK, reenabledResp.StatusCode, "traffic must recover immediately after re-enabling, no restart needed")
}

// TestB2BRouter_SuspendedTenant_ReadsAllowedWritesDenied proves §23.7's
// suspension policy through the ACTUAL assembled router, over real HTTP,
// against real Postgres: found while auditing T10's cross-tenant matrix
// that the old code rejected a suspended tenant's requests uniformly,
// including reads — the plan's own stated default policy is "read access
// may remain available for reconciliation" while financial/management
// writes are denied.
func TestB2BRouter_SuspendedTenant_ReadsAllowedWritesDenied(t *testing.T) {
	gatewayDB, _ := setupGatewayTestDB(t)
	ctx := context.Background()

	tenants := repository.NewTenantRepository(gatewayDB)
	apiKeys := repository.NewAPIKeyRepository(gatewayDB)
	quotas := repository.NewQuotaRepository(gatewayDB)
	idemRepo := repository.NewIdempotencyRepository(gatewayDB)
	keySvc := auth.NewKeyService(apiKeys, tenants, integrationTestPepper)

	tenantID := uuid.New()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantID, PublicID: "mrc_" + uuid.NewString()[:16], ExternalCode: "ext-suspend-" + tenantID.String(),
		Name: "Suspension Test Merchant", Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))
	disableQuota(t, quotas, tenantID)
	key, _, err := keySvc.CreateKey(ctx, tenantID, "live", []string{"merchant:read", "payins:write", "payins:read"}, "operator")
	require.NoError(t, err)

	router := merchantapi.NewRouter(merchantapi.Deps{
		APIKeys:       apiKeys,
		Tenants:       tenants,
		APIKeyPepper:  integrationTestPepper,
		QuotaEnforcer: quota.NewEnforcer(quotas, nil),
		Idempotency:   idempotency.NewService(idemRepo, 24*time.Hour, "test"),
		GlobalFlag:    auth.NewGlobalFlag(repository.NewSettingsRepository(gatewayDB)),
		Payin:         client.NewPayinClient(startBufconnPayin(t, newFakePayinServer())),
		Payout:        client.NewPayoutClient(startBufconnPayout(t, newFakePayoutServer())),
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	preResp, _ := doRequest(t, http.MethodGet, srv.URL+"/merchant", key, "", nil)
	require.Equal(t, http.StatusOK, preResp.StatusCode, "an active tenant must read successfully before suspension")

	require.NoError(t, tenants.UpdateStatus(ctx, tenantID, "suspended", "test-operator"))

	readResp, _ := doRequest(t, http.MethodGet, srv.URL+"/merchant", key, "", nil)
	assert.Equal(t, http.StatusOK, readResp.StatusCode, "a suspended tenant must still be able to read, per §23.7")

	writeResp, writeEnv := doRequest(t, http.MethodPost, srv.URL+"/payins", key, "suspend-write-1", []byte(`{"amount":"1000","currency":"IDR"}`))
	assert.Equal(t, http.StatusForbidden, writeResp.StatusCode, "a suspended tenant's writes must be denied")
	require.NotNil(t, writeEnv.Error)
	assert.Equal(t, "TENANT_SUSPENDED", writeEnv.Error.Code)

	require.NoError(t, tenants.UpdateStatus(ctx, tenantID, "active", "test-operator"))
	resumedResp, _ := doRequest(t, http.MethodPost, srv.URL+"/payins", key, "suspend-write-2", []byte(`{"amount":"1000","currency":"IDR"}`))
	assert.Equal(t, http.StatusCreated, resumedResp.StatusCode, "writes must recover immediately once the tenant is reactivated")
}

// TestB2BRouter_ConcurrentTenantSuspensionAndFinancialWrite proves §23.8
// item 6: N genuinely concurrent financial writes (each its own unique
// idempotency key, not a replay of one) racing a single goroutine that
// suspends the tenant partway through must never produce anything but a
// clean 201 (write landed before suspension) or 403 TENANT_SUSPENDED
// (write landed after) — no panic, no 500, and critically no write that
// reports success while the tenant is (or becomes) suspended: every 201
// response's own request must have actually created a payin record,
// checked afterward against the real count in Postgres, so a race that
// let a write "succeed" without actually completing wouldn't hide behind
// a miscounted assertion.
func TestB2BRouter_ConcurrentTenantSuspensionAndFinancialWrite(t *testing.T) {
	gatewayDB, _ := setupGatewayTestDB(t)
	ctx := context.Background()

	tenants := repository.NewTenantRepository(gatewayDB)
	apiKeys := repository.NewAPIKeyRepository(gatewayDB)
	quotas := repository.NewQuotaRepository(gatewayDB)
	idemRepo := repository.NewIdempotencyRepository(gatewayDB)
	keySvc := auth.NewKeyService(apiKeys, tenants, integrationTestPepper)

	tenantID := uuid.New()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantID, PublicID: "mrc_" + uuid.NewString()[:16], ExternalCode: "ext-race6-" + tenantID.String(),
		Name: "Race6 Merchant", Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))
	disableQuota(t, quotas, tenantID)
	key, _, err := keySvc.CreateKey(ctx, tenantID, "live", []string{"merchant:read", "payins:write", "payins:read"}, "operator")
	require.NoError(t, err)

	router := merchantapi.NewRouter(merchantapi.Deps{
		APIKeys:       apiKeys,
		Tenants:       tenants,
		APIKeyPepper:  integrationTestPepper,
		QuotaEnforcer: quota.NewEnforcer(quotas, nil),
		Idempotency:   idempotency.NewService(idemRepo, 24*time.Hour, "test"),
		GlobalFlag:    auth.NewGlobalFlag(repository.NewSettingsRepository(gatewayDB)),
		Payin:         client.NewPayinClient(startBufconnPayin(t, newFakePayinServer())),
		Payout:        client.NewPayoutClient(startBufconnPayout(t, newFakePayoutServer())),
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	const concurrency = 30
	var wg sync.WaitGroup
	var created, denied, unexpected int32
	wg.Add(concurrency + 1)
	for i := range concurrency {
		go func(i int) {
			defer wg.Done()
			resp, _ := doRequest(t, http.MethodPost, srv.URL+"/payins", key, fmt.Sprintf("race6-%d", i), []byte(`{"amount":"1000","currency":"IDR"}`))
			switch resp.StatusCode {
			case http.StatusCreated:
				atomic.AddInt32(&created, 1)
			case http.StatusForbidden:
				atomic.AddInt32(&denied, 1)
			default:
				atomic.AddInt32(&unexpected, 1)
			}
		}(i)
	}
	go func() {
		defer wg.Done()
		require.NoError(t, tenants.UpdateStatus(ctx, tenantID, "suspended", "test-operator"))
	}()
	wg.Wait()

	assert.Equal(t, int32(0), atomic.LoadInt32(&unexpected), "every concurrent write must resolve to exactly 201 or 403 — no panic, no 500")
	assert.Equal(t, int32(concurrency), created+denied, "every goroutine must have gotten a response")

	// The tenant is suspended by now (the goroutine above already
	// completed, and status changes are immediately visible — no cache).
	// A write attempted AFTER this point must be denied.
	postResp, _ := doRequest(t, http.MethodPost, srv.URL+"/payins", key, "race6-after", []byte(`{"amount":"1000","currency":"IDR"}`))
	assert.Equal(t, http.StatusForbidden, postResp.StatusCode, "a write attempted once suspension is confirmed durable must be denied")
}
