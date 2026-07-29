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
// migration's own GRANT statement depends on.
func setupGatewayTestDB(t *testing.T) *database.DBSQL {
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
	return db
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
	for _, class := range []string{"payin", "payout", "read"} {
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
	db := setupGatewayTestDB(t)
	tenants := repository.NewTenantRepository(db)
	apiKeys := repository.NewAPIKeyRepository(db)
	quotas := repository.NewQuotaRepository(db)
	idemRepo := repository.NewIdempotencyRepository(db)
	keySvc := auth.NewKeyService(apiKeys, integrationTestPepper)

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
