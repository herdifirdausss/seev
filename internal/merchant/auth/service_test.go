package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/merchant/model"
)

// TestKeyService_CreateKey_RejectsEnvironmentMismatch proves T10's §23.7
// fix: found live while auditing cross-tenant coverage that CreateKey took
// tenantID and environment as fully independent arguments, so an operator
// could issue a "live" key for a tenant that was created (and auto-
// activated) as "sandbox" — completely bypassing the maker/checker
// draft->active approval a real live tenant is supposed to require. See
// ErrEnvironmentMismatch's own doc comment in service.go for the full
// story.
func TestKeyService_CreateKey_RejectsEnvironmentMismatch(t *testing.T) {
	keys, tenants := newFakeKeyRepo(), newFakeTenantRepo()
	svc := NewKeyService(keys, tenants, testPepper)
	ctx := context.Background()

	sandboxTenant := uuid.New()
	tenants.byID[sandboxTenant] = model.Tenant{ID: sandboxTenant, Status: "active", Environment: "sandbox"}
	_, _, err := svc.CreateKey(ctx, sandboxTenant, "live", []string{"merchant:read"}, "operator")
	require.ErrorIs(t, err, ErrEnvironmentMismatch, "a sandbox tenant must never receive a live key")

	liveTenant := uuid.New()
	tenants.byID[liveTenant] = model.Tenant{ID: liveTenant, Status: "active", Environment: "live"}
	_, _, err = svc.CreateKey(ctx, liveTenant, "sandbox", []string{"merchant:read"}, "operator")
	require.ErrorIs(t, err, ErrEnvironmentMismatch, "a live tenant must never receive a sandbox key")

	_, _, err = svc.CreateKey(ctx, sandboxTenant, "sandbox", []string{"merchant:read"}, "operator")
	assert.NoError(t, err, "a matching environment must still succeed")

	_, _, err = svc.CreateKey(ctx, liveTenant, "live", []string{"merchant:read"}, "operator")
	assert.NoError(t, err, "a matching environment must still succeed")
}

// TestRequireMerchantAuth_TenantKeyEnvironmentMismatch_FailsClosed proves
// the middleware's own defense-in-depth layer: even a key that predates
// CreateKey's new guard (constructed directly here, bypassing CreateKey
// entirely, standing in for a legacy/pre-fix row) must still be rejected
// at request time if its Environment disagrees with its tenant's.
func TestRequireMerchantAuth_TenantKeyEnvironmentMismatch_FailsClosed(t *testing.T) {
	keys, tenants := newFakeKeyRepo(), newFakeTenantRepo()

	generated, err := GenerateKey("live")
	require.NoError(t, err)
	digest, err := Digest(testPepper, generated.Plaintext)
	require.NoError(t, err)

	tenantID := uuid.New()
	tenants.byID[tenantID] = model.Tenant{ID: tenantID, Status: "active", Environment: "sandbox"}
	keys.byPrefix[generated.PublicPrefix] = model.APIKey{
		ID: uuid.New(), TenantID: tenantID, PublicPrefix: generated.PublicPrefix, SecretDigest: digest,
		Environment: "live", Status: "active", Scopes: []string{"merchant:read"},
	}

	called := false
	handler := RequireMerchantAuth(keys, tenants, testPepper)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newAuthedRequest(generated.Plaintext))

	assert.Equal(t, http.StatusUnauthorized, rec.Code, "a live key on a sandbox tenant must fail closed even if it predates CreateKey's own guard")
	assert.False(t, called, "the wrapped handler must never run")
}
