package merchant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/merchant/auth"
	"github.com/herdifirdausss/seev/internal/merchant/lifecycle"
	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/internal/merchant/webhook"
	"github.com/herdifirdausss/seev/pkg/cryptox"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

const testJWTSecret = "test-jwt-secret-at-least-32-chars-long"

func testCryptoxRing(t *testing.T) *cryptox.Ring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 5)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	return ring
}

// ─── Hand-written fakes (this codebase's established no-gomock convention
// for internal/merchant subpackages) ──────────────────────────────────────

type fakeTenantRepo struct {
	mu      sync.Mutex
	tenants map[uuid.UUID]model.Tenant
}

func newFakeTenantRepo() *fakeTenantRepo {
	return &fakeTenantRepo{tenants: map[uuid.UUID]model.Tenant{}}
}

func (f *fakeTenantRepo) Create(_ context.Context, t model.Tenant) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tenants[t.ID] = t
	return nil
}

func (f *fakeTenantRepo) GetByID(_ context.Context, id uuid.UUID) (model.Tenant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tenants[id]
	if !ok {
		return model.Tenant{}, repository.ErrNotFound
	}
	return t, nil
}

func (f *fakeTenantRepo) GetByPublicID(_ context.Context, publicID string) (model.Tenant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tenants {
		if t.PublicID == publicID {
			return t, nil
		}
	}
	return model.Tenant{}, repository.ErrNotFound
}

func (f *fakeTenantRepo) UpdateStatus(_ context.Context, id uuid.UUID, status, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tenants[id]
	if !ok {
		return repository.ErrNotFound
	}
	t.Status = status
	switch status {
	case "active":
		t.ActivatedBy = &actor
	case "suspended":
		t.SuspendedBy = &actor
	}
	f.tenants[id] = t
	return nil
}

func (f *fakeTenantRepo) SetPrimaryAccount(_ context.Context, id, accountID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tenants[id]
	if !ok {
		return repository.ErrNotFound
	}
	t.PrimaryAccountID = &accountID
	f.tenants[id] = t
	return nil
}

type fakeAPIKeyRepo struct {
	mu   sync.Mutex
	keys map[uuid.UUID]model.APIKey
}

func newFakeAPIKeyRepo() *fakeAPIKeyRepo {
	return &fakeAPIKeyRepo{keys: map[uuid.UUID]model.APIKey{}}
}

func (f *fakeAPIKeyRepo) Create(_ context.Context, k model.APIKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys[k.ID] = k
	return nil
}

func (f *fakeAPIKeyRepo) GetActiveByPrefix(_ context.Context, prefix string) (model.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range f.keys {
		if k.PublicPrefix == prefix && k.Status == "active" {
			return k, nil
		}
	}
	return model.APIKey{}, repository.ErrNotFound
}

func (f *fakeAPIKeyRepo) ListByTenant(_ context.Context, tenantID uuid.UUID) ([]model.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.APIKey
	for _, k := range f.keys {
		if k.TenantID == tenantID {
			out = append(out, k)
		}
	}
	return out, nil
}

func (f *fakeAPIKeyRepo) Revoke(_ context.Context, tenantID, keyID uuid.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.keys[keyID]
	if !ok || k.TenantID != tenantID {
		return repository.ErrNotFound
	}
	k.Status = "revoked"
	f.keys[keyID] = k
	return nil
}

func (f *fakeAPIKeyRepo) TouchLastUsed(_ context.Context, keyID uuid.UUID) error {
	return nil
}

type fakeQuotaRepo struct {
	mu       sync.Mutex
	policies map[string]model.QuotaPolicy
}

func newFakeQuotaRepo() *fakeQuotaRepo {
	return &fakeQuotaRepo{policies: map[string]model.QuotaPolicy{}}
}

func quotaKey(tenantID uuid.UUID, class string) string { return tenantID.String() + "|" + class }

func (f *fakeQuotaRepo) Upsert(_ context.Context, p model.QuotaPolicy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policies[quotaKey(p.TenantID, p.QuotaClass)] = p
	return nil
}

func (f *fakeQuotaRepo) GetByTenantAndClass(_ context.Context, tenantID uuid.UUID, class string) (model.QuotaPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.policies[quotaKey(tenantID, class)]
	if !ok {
		return model.QuotaPolicy{}, repository.ErrNotFound
	}
	return p, nil
}

// fakeLifecycleRepo mirrors internal/merchant/lifecycle's own test fake —
// duplicated here rather than exported cross-package, matching this
// codebase's per-package hand-written-fake convention.
type fakeLifecycleRepo struct {
	mu       sync.Mutex
	requests map[uuid.UUID]model.TenantLifecycleRequest
	pending  map[string]uuid.UUID
}

func newFakeLifecycleRepo() *fakeLifecycleRepo {
	return &fakeLifecycleRepo{requests: map[uuid.UUID]model.TenantLifecycleRequest{}, pending: map[string]uuid.UUID{}}
}

func (f *fakeLifecycleRepo) Create(_ context.Context, req model.TenantLifecycleRequest) (bool, model.TenantLifecycleRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := req.TenantID.String() + "|" + req.Action
	if id, ok := f.pending[key]; ok {
		return false, f.requests[id], nil
	}
	req.Status = "pending"
	f.requests[req.ID] = req
	f.pending[key] = req.ID
	return true, model.TenantLifecycleRequest{}, nil
}

func (f *fakeLifecycleRepo) GetByID(_ context.Context, id uuid.UUID) (model.TenantLifecycleRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	req, ok := f.requests[id]
	if !ok {
		return model.TenantLifecycleRequest{}, repository.ErrNotFound
	}
	return req, nil
}

func (f *fakeLifecycleRepo) GetPending(_ context.Context, tenantID uuid.UUID, action string) (model.TenantLifecycleRequest, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.pending[tenantID.String()+"|"+action]
	if !ok {
		return model.TenantLifecycleRequest{}, false, nil
	}
	return f.requests[id], true, nil
}

func (f *fakeLifecycleRepo) List(_ context.Context, tenantID uuid.UUID, status string, limit int) ([]model.TenantLifecycleRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.TenantLifecycleRequest
	for _, req := range f.requests {
		if req.TenantID == tenantID && (status == "" || req.Status == status) {
			out = append(out, req)
		}
	}
	return out, nil
}

func (f *fakeLifecycleRepo) Decide(_ context.Context, id uuid.UUID, status, approvedBy string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	req, ok := f.requests[id]
	if !ok || req.Status != "pending" {
		return false, nil
	}
	req.Status, req.ApprovedBy = status, approvedBy
	f.requests[id] = req
	delete(f.pending, req.TenantID.String()+"|"+req.Action)
	return true, nil
}

// fakeWebhookRepo implements only what Service.CreateEndpoint/
// ListEndpoints/ListDeliveries actually exercise in this file's own
// onboarding-flow test — every other method exists solely to satisfy
// repository.WebhookRepository and is never called here (T7's own
// internal/merchant/webhook package already has full coverage of the
// relay/consumer/replay paths; this fake exists only to let AdminRouter's
// webhook endpoints be reached in a white-box HTTP test).
type fakeWebhookRepo struct {
	mu        sync.Mutex
	endpoints map[uuid.UUID]model.WebhookEndpoint
}

func newFakeWebhookRepo() *fakeWebhookRepo {
	return &fakeWebhookRepo{endpoints: map[uuid.UUID]model.WebhookEndpoint{}}
}

func (f *fakeWebhookRepo) CreateEndpoint(_ context.Context, e model.WebhookEndpoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endpoints[e.ID] = e
	return nil
}

func (f *fakeWebhookRepo) GetEndpoint(_ context.Context, tenantID, endpointID uuid.UUID) (model.WebhookEndpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.endpoints[endpointID]
	if !ok || e.TenantID != tenantID {
		return model.WebhookEndpoint{}, repository.ErrNotFound
	}
	return e, nil
}

func (f *fakeWebhookRepo) ListEndpoints(_ context.Context, tenantID uuid.UUID) ([]model.WebhookEndpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.WebhookEndpoint
	for _, e := range f.endpoints {
		if e.TenantID == tenantID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeWebhookRepo) UpdateEndpoint(_ context.Context, e model.WebhookEndpoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.endpoints[e.ID]; !ok {
		return repository.ErrNotFound
	}
	f.endpoints[e.ID] = e
	return nil
}

func (f *fakeWebhookRepo) DeleteEndpoint(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeWebhookRepo) DisableEndpoint(context.Context, uuid.UUID) error           { return nil }
func (f *fakeWebhookRepo) CreateEvent(context.Context, model.WebhookEvent) error      { return nil }
func (f *fakeWebhookRepo) GetEventBySource(context.Context, uuid.UUID, uuid.UUID, string) (model.WebhookEvent, bool, error) {
	return model.WebhookEvent{}, false, nil
}
func (f *fakeWebhookRepo) GetEventByID(context.Context, uuid.UUID) (model.WebhookEvent, error) {
	return model.WebhookEvent{}, repository.ErrNotFound
}
func (f *fakeWebhookRepo) CreateDelivery(context.Context, model.WebhookDelivery) (bool, error) {
	return false, nil
}
func (f *fakeWebhookRepo) CreateReplayDelivery(context.Context, model.WebhookDelivery) error {
	return nil
}
func (f *fakeWebhookRepo) GetDelivery(context.Context, uuid.UUID, uuid.UUID) (model.WebhookDelivery, error) {
	return model.WebhookDelivery{}, repository.ErrNotFound
}
func (f *fakeWebhookRepo) ListDeliveries(context.Context, uuid.UUID, int) ([]model.WebhookDelivery, error) {
	return nil, nil
}
func (f *fakeWebhookRepo) ListDue(context.Context, int) ([]model.WebhookDelivery, error) {
	return nil, nil
}
func (f *fakeWebhookRepo) ClaimDue(context.Context, int, string, time.Time) ([]model.WebhookDelivery, error) {
	return nil, nil
}
func (f *fakeWebhookRepo) MarkDelivered(context.Context, uuid.UUID, int) error { return nil }
func (f *fakeWebhookRepo) MarkFailedAttempt(context.Context, uuid.UUID, string, *int, any) error {
	return nil
}
func (f *fakeWebhookRepo) MarkDead(context.Context, uuid.UUID) error           { return nil }
func (f *fakeWebhookRepo) RecordAttempt(context.Context, model.WebhookAttempt) error { return nil }

// ─── Test harness ──────────────────────────────────────────────────────

func testModule(t *testing.T) (*Module, *fakeTenantRepo, *fakeAPIKeyRepo, *fakeQuotaRepo) {
	t.Helper()
	tenants := newFakeTenantRepo()
	apiKeys := newFakeAPIKeyRepo()
	quotas := newFakeQuotaRepo()
	lifecycleRepo := newFakeLifecycleRepo()
	webhooks := newFakeWebhookRepo()

	m := &Module{
		Tenants:          tenants,
		APIKeys:          apiKeys,
		Quotas:           quotas,
		KeyService:       auth.NewKeyService(apiKeys, "test-pepper-0123456789"),
		LifecycleService: lifecycle.NewService(lifecycleRepo, tenants),
		WebhookService:   webhook.NewService(webhooks, testCryptoxRing(t)),
	}
	return m, tenants, apiKeys, quotas
}

// authedRouter wraps AdminRouter with the same middleware.WithAuth chain
// cmd/gateway's own internal router uses (internal/handler/router.go) —
// matches internal/ledger/transport's own established test pattern of
// driving role gates through a REAL signed JWT rather than injecting
// claims into the context directly.
func authedRouter(m *Module) http.Handler {
	return middleware.WithAuth(testJWTSecret, "")(m.AdminRouter())
}

// tokenFor mints a distinct operator identity per role (email derived from
// the role itself) — this matters for maker-checker tests, since two
// different roles must also be two different actors for a cross-approval
// test to prove anything; use tokenForActor directly when a test needs
// the SAME actor to hold two roles (self-approval).
func tokenFor(t *testing.T, role string) string {
	t.Helper()
	return tokenForActor(t, role, role+"@example.test")
}

func tokenForActor(t *testing.T, role, email string) string {
	t.Helper()
	tok, err := middleware.GenerateToken(testJWTSecret, middleware.Claims{
		UserID: "00000000-0000-0000-0000-000000000099", Email: email, Role: role,
		Exp: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	return tok
}

func doRequestWithToken(t *testing.T, handler http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = strings.NewReader(string(b))
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func doRequest(t *testing.T, handler http.Handler, method, path, role string, body any) *httptest.ResponseRecorder {
	t.Helper()
	token := ""
	if role != "" {
		token = tokenFor(t, role)
	}
	return doRequestWithToken(t, handler, method, path, token, body)
}

// ─── Tests ─────────────────────────────────────────────────────────────

func TestAdminRouter_CreateTenant_RequiresMaker(t *testing.T) {
	m, _, _, _ := testModule(t)
	router := authedRouter(m)

	rec := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants", "user", map[string]any{
		"external_code": "T1", "name": "Tenant", "environment": "sandbox", "default_currency": "IDR",
	})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAdminRouter_CreateTenant_NoTokenUnauthorized(t *testing.T) {
	m, _, _, _ := testModule(t)
	router := authedRouter(m)

	rec := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants", "", map[string]any{
		"external_code": "T1", "name": "Tenant", "environment": "sandbox", "default_currency": "IDR",
	})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAdminRouter_CreateSandboxTenant_IsImmediatelyActive(t *testing.T) {
	m, _, _, _ := testModule(t)
	router := authedRouter(m)

	rec := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants", "admin_maker", map[string]any{
		"external_code": "T1", "name": "Tenant", "environment": "sandbox", "default_currency": "IDR",
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out struct {
		Data model.Tenant `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "active", out.Data.Status, "a sandbox tenant needs no checker gate — it goes active immediately")
}

func TestAdminRouter_CreateLiveTenant_StartsDraft(t *testing.T) {
	m, _, _, _ := testModule(t)
	router := authedRouter(m)

	rec := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants", "admin_maker", map[string]any{
		"external_code": "T2", "name": "Tenant", "environment": "live", "default_currency": "IDR",
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out struct {
		Data model.Tenant `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "draft", out.Data.Status, "a live tenant must start in draft, pending a separate checker-approved activation")
}

func TestAdminRouter_SuspendTenant_MakerOnly(t *testing.T) {
	m, tenants, _, _ := testModule(t)
	router := authedRouter(m)
	tenantID := generalutil.NewV7()
	require.NoError(t, tenants.Create(context.Background(), model.Tenant{ID: tenantID, Status: "active"}))

	rec := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants/"+tenantID.String()+"/suspend", "admin_checker", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code, "suspend has no checker allowance — maker only")

	rec2 := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants/"+tenantID.String()+"/suspend", "admin_maker", nil)
	assert.Equal(t, http.StatusNoContent, rec2.Code)

	tenant, err := tenants.GetByID(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "suspended", tenant.Status)
}

func TestAdminRouter_LifecycleActivate_NonCheckerRoleRejected(t *testing.T) {
	m, tenants, _, _ := testModule(t)
	router := authedRouter(m)
	tenantID := generalutil.NewV7()
	require.NoError(t, tenants.Create(context.Background(), model.Tenant{ID: tenantID, Status: "draft"}))

	proposeRec := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants/"+tenantID.String()+"/lifecycle/propose", "admin_maker", map[string]any{
		"action": "activate", "reason": "KYB approved",
	})
	require.Equal(t, http.StatusCreated, proposeRec.Code, proposeRec.Body.String())
	var proposed struct {
		Data model.TenantLifecycleRequest `json:"data"`
	}
	require.NoError(t, json.Unmarshal(proposeRec.Body.Bytes(), &proposed))

	notChecker := doRequest(t, router, http.MethodPost, "/admin/gateway/lifecycle/"+proposed.Data.ID.String()+"/approve", "admin_maker", nil)
	assert.Equal(t, http.StatusForbidden, notChecker.Code, "the maker role alone must not satisfy the checker gate")
}

func TestAdminRouter_LifecycleActivate_SelfApprovalRejected(t *testing.T) {
	// A single "admin" superuser identity holds both maker and checker
	// capability — the ROLE gate alone would let this actor approve its
	// own proposal, so the self-approval check inside lifecycle.Approve is
	// what must catch it.
	m, tenants, _, _ := testModule(t)
	router := authedRouter(m)
	tenantID := generalutil.NewV7()
	require.NoError(t, tenants.Create(context.Background(), model.Tenant{ID: tenantID, Status: "draft"}))
	token := tokenForActor(t, "admin", "sole-operator@example.test")

	proposeRec := doRequestWithToken(t, router, http.MethodPost, "/admin/gateway/tenants/"+tenantID.String()+"/lifecycle/propose", token, map[string]any{
		"action": "activate", "reason": "KYB approved",
	})
	require.Equal(t, http.StatusCreated, proposeRec.Code, proposeRec.Body.String())
	var proposed struct {
		Data model.TenantLifecycleRequest `json:"data"`
	}
	require.NoError(t, json.Unmarshal(proposeRec.Body.Bytes(), &proposed))

	selfApprove := doRequestWithToken(t, router, http.MethodPost, "/admin/gateway/lifecycle/"+proposed.Data.ID.String()+"/approve", token, nil)
	assert.Equal(t, http.StatusForbidden, selfApprove.Code, selfApprove.Body.String())
}

func TestAdminRouter_LifecycleActivate_DifferentCheckerSucceeds(t *testing.T) {
	m, tenants, _, _ := testModule(t)
	router := authedRouter(m)
	tenantID := generalutil.NewV7()
	require.NoError(t, tenants.Create(context.Background(), model.Tenant{ID: tenantID, Status: "draft"}))

	proposeRec := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants/"+tenantID.String()+"/lifecycle/propose", "admin_maker", map[string]any{
		"action": "activate", "reason": "KYB approved",
	})
	require.Equal(t, http.StatusCreated, proposeRec.Code, proposeRec.Body.String())
	var proposed struct {
		Data model.TenantLifecycleRequest `json:"data"`
	}
	require.NoError(t, json.Unmarshal(proposeRec.Body.Bytes(), &proposed))

	approve := doRequest(t, router, http.MethodPost, "/admin/gateway/lifecycle/"+proposed.Data.ID.String()+"/approve", "admin_checker", nil)
	require.Equal(t, http.StatusOK, approve.Code, approve.Body.String())

	tenant, err := tenants.GetByID(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "active", tenant.Status)
}

func TestAdminRouter_CreateKey_UnknownScopeIsBadRequest(t *testing.T) {
	m, tenants, _, _ := testModule(t)
	router := authedRouter(m)
	tenantID := generalutil.NewV7()
	require.NoError(t, tenants.Create(context.Background(), model.Tenant{ID: tenantID, Status: "active"}))

	rec := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants/"+tenantID.String()+"/keys", "admin_maker", map[string]any{
		"environment": "sandbox", "scopes": []string{"not-a-real-scope"},
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "an unknown scope must map to 400, not a bare 500")
}

func TestAdminRouter_CreateKey_ReturnsPlaintextOnce(t *testing.T) {
	m, tenants, _, _ := testModule(t)
	router := authedRouter(m)
	tenantID := generalutil.NewV7()
	require.NoError(t, tenants.Create(context.Background(), model.Tenant{ID: tenantID, Status: "active"}))

	rec := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants/"+tenantID.String()+"/keys", "admin_maker", map[string]any{
		"environment": "sandbox", "scopes": []string{"merchant:read"},
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out struct {
		Data struct {
			Plaintext string    `json:"plaintext"`
			KeyID     uuid.UUID `json:"key_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.NotEmpty(t, out.Data.Plaintext)

	listRec := doRequest(t, router, http.MethodGet, "/admin/gateway/tenants/"+tenantID.String()+"/keys", "admin", nil)
	require.Equal(t, http.StatusOK, listRec.Code)
	assert.NotContains(t, listRec.Body.String(), out.Data.Plaintext, "the plaintext key must never appear in the list response")
}

func TestAdminRouter_UpdateQuota_AboveBaselineRequiresChecker(t *testing.T) {
	m, tenants, _, _ := testModule(t)
	router := authedRouter(m)
	tenantID := generalutil.NewV7()
	require.NoError(t, tenants.Create(context.Background(), model.Tenant{ID: tenantID, Status: "active"}))

	rec := doRequest(t, router, http.MethodPut, "/admin/gateway/tenants/"+tenantID.String()+"/quota", "admin_maker", map[string]any{
		"requests_per_minute": 500, "burst": 500, "is_enabled": true,
	})
	assert.Equal(t, http.StatusForbidden, rec.Code)

	rec2 := doRequest(t, router, http.MethodPut, "/admin/gateway/tenants/"+tenantID.String()+"/quota", "admin_checker", map[string]any{
		"requests_per_minute": 500, "burst": 500, "is_enabled": true,
	})
	assert.Equal(t, http.StatusOK, rec2.Code, rec2.Body.String())
}

func TestAdminRouter_UpdateQuota_AtBaselineAllowsMaker(t *testing.T) {
	m, tenants, _, _ := testModule(t)
	router := authedRouter(m)
	tenantID := generalutil.NewV7()
	require.NoError(t, tenants.Create(context.Background(), model.Tenant{ID: tenantID, Status: "active"}))

	rec := doRequest(t, router, http.MethodPut, "/admin/gateway/tenants/"+tenantID.String()+"/quota", "admin_maker", map[string]any{
		"requests_per_minute": 60, "burst": 60, "is_enabled": true,
	})
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestAdminRouter_ProvisionAccount_UnavailableWithoutLedgerClient(t *testing.T) {
	m, tenants, _, _ := testModule(t)
	router := authedRouter(m)
	tenantID := generalutil.NewV7()
	require.NoError(t, tenants.Create(context.Background(), model.Tenant{ID: tenantID, Status: "active", DefaultCurrency: "IDR"}))

	rec := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants/"+tenantID.String()+"/account", "admin_maker", nil)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "m.Ledger is nil in this test module — must degrade explicitly, not panic")
}

// TestAdminRouter_FullSandboxOnboardingFlow drives T8's own named acceptance
// criterion end to end, through the real HTTP handlers and the real JWT
// auth middleware chain (matching cmd/gateway's own wiring): create a
// sandbox tenant -> create an API key (one-time secret) -> create a
// webhook endpoint (one-time secret) -> confirm neither secret is ever
// re-exposed by a subsequent read -> list deliveries (empty, but reachable).
// Account provisioning is skipped here (no ledger client in this fake
// module) — the ledger side of that call is proven separately by
// pkg/ledgerclient's own tests and this file's UnavailableWithoutLedgerClient
// case above.
func TestAdminRouter_FullSandboxOnboardingFlow(t *testing.T) {
	m, _, _, _ := testModule(t)
	router := authedRouter(m)

	createTenantRec := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants", "admin_maker", map[string]any{
		"external_code": "ONBOARD1", "name": "Onboarding Test Co", "environment": "sandbox", "default_currency": "IDR",
	})
	require.Equal(t, http.StatusCreated, createTenantRec.Code, createTenantRec.Body.String())
	var tenantOut struct {
		Data model.Tenant `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createTenantRec.Body.Bytes(), &tenantOut))
	require.Equal(t, "active", tenantOut.Data.Status)
	tenantID := tenantOut.Data.ID.String()

	keyRec := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants/"+tenantID+"/keys", "admin_maker", map[string]any{
		"environment": "sandbox", "scopes": []string{"merchant:read", "transactions:read"},
	})
	require.Equal(t, http.StatusCreated, keyRec.Code, keyRec.Body.String())
	var keyOut struct {
		Data struct {
			Plaintext string `json:"plaintext"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(keyRec.Body.Bytes(), &keyOut))
	require.NotEmpty(t, keyOut.Data.Plaintext)

	webhookRec := doRequest(t, router, http.MethodPost, "/admin/gateway/tenants/"+tenantID+"/webhooks", "admin_maker", map[string]any{
		"url": "https://merchant.example.test/webhooks", "environment": "sandbox",
		"subscribed_events": []string{"transaction.posted.v1"},
	})
	require.Equal(t, http.StatusCreated, webhookRec.Code, webhookRec.Body.String())
	var webhookOut struct {
		Data struct {
			PlaintextSecret string `json:"plaintext_secret"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(webhookRec.Body.Bytes(), &webhookOut))
	require.NotEmpty(t, webhookOut.Data.PlaintextSecret)

	keyListRec := doRequest(t, router, http.MethodGet, "/admin/gateway/tenants/"+tenantID+"/keys", "admin", nil)
	require.Equal(t, http.StatusOK, keyListRec.Code)
	assert.NotContains(t, keyListRec.Body.String(), keyOut.Data.Plaintext)

	webhookListRec := doRequest(t, router, http.MethodGet, "/admin/gateway/tenants/"+tenantID+"/webhooks", "admin", nil)
	require.Equal(t, http.StatusOK, webhookListRec.Code)
	assert.NotContains(t, webhookListRec.Body.String(), webhookOut.Data.PlaintextSecret, "the webhook secret must never appear in the endpoint list response")

	deliveriesRec := doRequest(t, router, http.MethodGet, "/admin/gateway/tenants/"+tenantID+"/deliveries", "admin", nil)
	assert.Equal(t, http.StatusOK, deliveriesRec.Code)
}
