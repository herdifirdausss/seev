package quota

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/auth"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/model"
)

func newTestRequest(t *testing.T, tenantID uuid.UUID) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/b2b/v1/transfers", nil)
	principal := auth.Principal{TenantID: tenantID, KeyID: uuid.New(), Environment: "sandbox", Scopes: []string{"transfers:write"}}
	return req.WithContext(auth.WithPrincipal(req.Context(), principal))
}

func TestRequireQuota_NoPrincipal_Unauthorized(t *testing.T) {
	repo := newFakeQuotaRepo()
	enforcer := NewEnforcer(repo, newMiniredisClient(t))
	handlerCalled := false
	handler := RequireQuota(enforcer, "transfers", true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/b2b/v1/transfers", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, handlerCalled)
}

func TestRequireQuota_AllowsAndSetsHeaders(t *testing.T) {
	repo := newFakeQuotaRepo()
	tenantID := uuid.New()
	require.NoError(t, repo.Upsert(context.Background(), model.QuotaPolicy{
		TenantID: tenantID, QuotaClass: "transfers", RequestsPerMinute: 60, Burst: 5, IsEnabled: true,
	}))
	enforcer := NewEnforcer(repo, newMiniredisClient(t))
	handlerCalled := false
	handler := RequireQuota(enforcer, "transfers", true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusCreated)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newTestRequest(t, tenantID))
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.True(t, handlerCalled)
	assert.Equal(t, "60", rec.Header().Get("RateLimit-Limit"))
	assert.NotEmpty(t, rec.Header().Get("RateLimit-Remaining"))
	assert.NotEmpty(t, rec.Header().Get("RateLimit-Reset"))
}

func TestRequireQuota_OverBurst_429WithRetryAfter(t *testing.T) {
	repo := newFakeQuotaRepo()
	tenantID := uuid.New()
	require.NoError(t, repo.Upsert(context.Background(), model.QuotaPolicy{
		TenantID: tenantID, QuotaClass: "transfers", RequestsPerMinute: 60, Burst: 1, IsEnabled: true,
	}))
	enforcer := NewEnforcer(repo, newMiniredisClient(t))
	handler := RequireQuota(enforcer, "transfers", true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, newTestRequest(t, tenantID))
	require.Equal(t, http.StatusCreated, allowedResponse.Code)

	rejectedResponse := httptest.NewRecorder()
	handlerCalled := false
	rejectedHandler := RequireQuota(enforcer, "transfers", true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
	}))
	rejectedHandler.ServeHTTP(rejectedResponse, newTestRequest(t, tenantID))
	assert.Equal(t, http.StatusTooManyRequests, rejectedResponse.Code)
	assert.False(t, handlerCalled, "the wrapped handler must never run once the quota is exceeded")
	assert.NotEmpty(t, rejectedResponse.Header().Get("Retry-After"))
}

func TestRequireQuota_BackendUnavailable_WriteIs503WithRetryAfter(t *testing.T) {
	repo := newFakeQuotaRepo()
	tenantID := uuid.New()
	require.NoError(t, repo.Upsert(context.Background(), model.QuotaPolicy{
		TenantID: tenantID, QuotaClass: "transfers", RequestsPerMinute: 60, Burst: 5, IsEnabled: true,
	}))
	enforcer := NewEnforcer(repo, nil) // nil redis client => backend unavailable
	handlerCalled := false
	handler := RequireQuota(enforcer, "transfers", true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newTestRequest(t, tenantID))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.False(t, handlerCalled, "a write must fail closed when the quota backend is unavailable")
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
}

func TestRequireQuota_BackendUnavailable_ReadDegradesButAllows(t *testing.T) {
	repo := newFakeQuotaRepo()
	tenantID := uuid.New()
	require.NoError(t, repo.Upsert(context.Background(), model.QuotaPolicy{
		TenantID: tenantID, QuotaClass: "reads", RequestsPerMinute: 60, Burst: 5, IsEnabled: true,
	}))
	enforcer := NewEnforcer(repo, nil)
	handlerCalled := false
	handler := RequireQuota(enforcer, "reads", false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newTestRequest(t, tenantID))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, handlerCalled, "a read must degrade-allow when the quota backend is unavailable, not block")
}
