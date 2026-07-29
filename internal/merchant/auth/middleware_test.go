package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
)

// fakeKeyRepo/fakeTenantRepo are minimal in-memory stand-ins for this
// package's own unit tests — no mockgen scaffolding, since only two
// methods each are exercised by RequireMerchantAuth.
type fakeKeyRepo struct {
	byPrefix map[string]model.APIKey
	touched  map[uuid.UUID]int
}

func newFakeKeyRepo() *fakeKeyRepo {
	return &fakeKeyRepo{byPrefix: map[string]model.APIKey{}, touched: map[uuid.UUID]int{}}
}

func (f *fakeKeyRepo) Create(context.Context, model.APIKey) error { return nil }
func (f *fakeKeyRepo) GetActiveByPrefix(_ context.Context, prefix string) (model.APIKey, error) {
	k, ok := f.byPrefix[prefix]
	if !ok || k.Status != "active" {
		return model.APIKey{}, repository.ErrNotFound
	}
	return k, nil
}
func (f *fakeKeyRepo) ListByTenant(context.Context, uuid.UUID) ([]model.APIKey, error) {
	return nil, nil
}
func (f *fakeKeyRepo) Revoke(_ context.Context, _, keyID uuid.UUID, _ string) error {
	for p, k := range f.byPrefix {
		if k.ID == keyID {
			k.Status = "revoked"
			f.byPrefix[p] = k
		}
	}
	return nil
}
func (f *fakeKeyRepo) TouchLastUsed(_ context.Context, keyID uuid.UUID) error {
	f.touched[keyID]++
	return nil
}

type fakeTenantRepo struct {
	byID map[uuid.UUID]model.Tenant
}

func newFakeTenantRepo() *fakeTenantRepo { return &fakeTenantRepo{byID: map[uuid.UUID]model.Tenant{}} }

func (f *fakeTenantRepo) Create(context.Context, model.Tenant) error { return nil }
func (f *fakeTenantRepo) GetByID(_ context.Context, id uuid.UUID) (model.Tenant, error) {
	t, ok := f.byID[id]
	if !ok {
		return model.Tenant{}, repository.ErrNotFound
	}
	return t, nil
}
func (f *fakeTenantRepo) GetByPublicID(context.Context, string) (model.Tenant, error) {
	return model.Tenant{}, repository.ErrNotFound
}
func (f *fakeTenantRepo) UpdateStatus(context.Context, uuid.UUID, string, string) error { return nil }
func (f *fakeTenantRepo) SetPrimaryAccount(context.Context, uuid.UUID, uuid.UUID) error { return nil }

const testPepper = "unit-test-pepper"

func seedKeyAndTenant(t *testing.T, keys *fakeKeyRepo, tenants *fakeTenantRepo, tenantStatus string, keyStatus string, expiresAt *time.Time, scopes []string) (plaintext string, tenantID, keyID uuid.UUID) {
	t.Helper()
	generated, err := GenerateKey("sandbox")
	require.NoError(t, err)
	digest, err := Digest(testPepper, generated.Plaintext)
	require.NoError(t, err)

	tenantID = uuid.New()
	keyID = uuid.New()
	tenants.byID[tenantID] = model.Tenant{ID: tenantID, Status: tenantStatus, Environment: "sandbox"}
	keys.byPrefix[generated.PublicPrefix] = model.APIKey{
		ID: keyID, TenantID: tenantID, PublicPrefix: generated.PublicPrefix, SecretDigest: digest,
		Environment: "sandbox", Status: keyStatus, ExpiresAt: expiresAt, Scopes: scopes,
	}
	return generated.Plaintext, tenantID, keyID
}

func newAuthedRequest(plaintext string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/b2b/merchant", nil)
	if plaintext != "" {
		req.Header.Set("Authorization", "Bearer "+plaintext)
	}
	return req
}

func TestRequireMerchantAuth_ValidKey_PopulatesPrincipal(t *testing.T) {
	keys, tenants := newFakeKeyRepo(), newFakeTenantRepo()
	plaintext, tenantID, keyID := seedKeyAndTenant(t, keys, tenants, "active", "active", nil, []string{"merchant:read"})

	var gotPrincipal Principal
	handler := RequireMerchantAuth(keys, tenants, testPepper)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPrincipal, _ = PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newAuthedRequest(plaintext))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, tenantID, gotPrincipal.TenantID)
	assert.Equal(t, keyID, gotPrincipal.KeyID)
	assert.Equal(t, []string{"merchant:read"}, gotPrincipal.Scopes)
}

// TestRequireMerchantAuth_FailsClosed proves T3's own required failure
// matrix: invalid, expired, revoked, wrong-environment, and
// suspended-tenant keys must ALL fail closed (T3 acceptance).
func TestRequireMerchantAuth_FailsClosed(t *testing.T) {
	past := time.Now().Add(-time.Hour)

	cases := []struct {
		name       string
		setup      func(t *testing.T, keys *fakeKeyRepo, tenants *fakeTenantRepo) string
		wantStatus int
	}{
		{
			name: "no Authorization header",
			setup: func(t *testing.T, keys *fakeKeyRepo, tenants *fakeTenantRepo) string {
				return ""
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "malformed key",
			setup: func(t *testing.T, keys *fakeKeyRepo, tenants *fakeTenantRepo) string {
				return "not-a-real-key"
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "unknown prefix",
			setup: func(t *testing.T, keys *fakeKeyRepo, tenants *fakeTenantRepo) string {
				generated, err := GenerateKey("sandbox")
				require.NoError(t, err)
				return generated.Plaintext // never inserted into keys.byPrefix
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "revoked key",
			setup: func(t *testing.T, keys *fakeKeyRepo, tenants *fakeTenantRepo) string {
				plaintext, _, _ := seedKeyAndTenant(t, keys, tenants, "active", "revoked", nil, nil)
				return plaintext
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "expired key",
			setup: func(t *testing.T, keys *fakeKeyRepo, tenants *fakeTenantRepo) string {
				plaintext, _, _ := seedKeyAndTenant(t, keys, tenants, "active", "active", &past, nil)
				return plaintext
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "suspended tenant",
			setup: func(t *testing.T, keys *fakeKeyRepo, tenants *fakeTenantRepo) string {
				plaintext, _, _ := seedKeyAndTenant(t, keys, tenants, "suspended", "active", nil, nil)
				return plaintext
			},
			wantStatus: http.StatusForbidden, // distinguishable per the failure matrix — the key itself is valid
		},
		{
			name: "draft tenant (not yet active)",
			setup: func(t *testing.T, keys *fakeKeyRepo, tenants *fakeTenantRepo) string {
				plaintext, _, _ := seedKeyAndTenant(t, keys, tenants, "draft", "active", nil, nil)
				return plaintext
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "closed tenant",
			setup: func(t *testing.T, keys *fakeKeyRepo, tenants *fakeTenantRepo) string {
				plaintext, _, _ := seedKeyAndTenant(t, keys, tenants, "closed", "active", nil, nil)
				return plaintext
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "tampered secret (correct prefix, wrong secret)",
			setup: func(t *testing.T, keys *fakeKeyRepo, tenants *fakeTenantRepo) string {
				plaintext, _, _ := seedKeyAndTenant(t, keys, tenants, "active", "active", nil, nil)
				return plaintext[:len(plaintext)-1] + "X"
			},
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keys, tenants := newFakeKeyRepo(), newFakeTenantRepo()
			plaintext := tc.setup(t, keys, tenants)

			called := false
			handler := RequireMerchantAuth(keys, tenants, testPepper)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, newAuthedRequest(plaintext))

			assert.Equal(t, tc.wantStatus, rec.Code)
			assert.False(t, called, "the wrapped handler must never run when authentication fails closed")
		})
	}
}

func TestRequireMerchantAuth_WrongEnvironmentPrefix_FailsClosed(t *testing.T) {
	keys, tenants := newFakeKeyRepo(), newFakeTenantRepo()
	// Construct a live-prefixed key whose STORED environment is sandbox —
	// simulates a data inconsistency the defensive check in the
	// middleware must still catch.
	generated, err := GenerateKey("live")
	require.NoError(t, err)
	digest, err := Digest(testPepper, generated.Plaintext)
	require.NoError(t, err)
	tenantID := uuid.New()
	tenants.byID[tenantID] = model.Tenant{ID: tenantID, Status: "active"}
	keys.byPrefix[generated.PublicPrefix] = model.APIKey{
		ID: uuid.New(), TenantID: tenantID, PublicPrefix: generated.PublicPrefix, SecretDigest: digest,
		Environment: "sandbox", Status: "active",
	}

	handler := RequireMerchantAuth(keys, tenants, testPepper)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newAuthedRequest(generated.Plaintext))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireMerchantAuth_RevocationTakesEffectImmediately(t *testing.T) {
	keys, tenants := newFakeKeyRepo(), newFakeTenantRepo()
	plaintext, tenantID, keyID := seedKeyAndTenant(t, keys, tenants, "active", "active", nil, nil)

	handler := RequireMerchantAuth(keys, tenants, testPepper)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newAuthedRequest(plaintext))
	require.Equal(t, http.StatusOK, rec.Code)

	require.NoError(t, keys.Revoke(context.Background(), tenantID, keyID, "operator"))

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, newAuthedRequest(plaintext))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "revocation must apply immediately, no positive cache (§8.4)")
}

func TestRequireScope_DeniesMissingScopeAndAllowsGranted(t *testing.T) {
	principal := Principal{TenantID: uuid.New(), Scopes: []string{"merchant:read"}}

	called := false
	handler := RequireScope("b2bGetMerchantV1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called)

	// A principal missing a required scope for a multi-scope operation
	// must be denied (403), not silently allowed via partial match.
	principal2 := Principal{TenantID: uuid.New(), Scopes: []string{"transfers:write"}} // missing transactions:read
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2 = req2.WithContext(WithPrincipal(req2.Context(), principal2))
	handler2 := RequireScope("b2bCreateTransferV1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run when scope is insufficient")
	}))
	rec2 := httptest.NewRecorder()
	handler2.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusForbidden, rec2.Code)
}

func TestRequireScope_NoPrincipalInContext_Unauthorized(t *testing.T) {
	handler := RequireScope("b2bGetMerchantV1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run without an authenticated principal")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
