//go:build integration

// Race test for T10b's §23.8 item 2: concurrent key rotation/revocation
// and an in-flight request, against real Postgres with genuine goroutine
// concurrency.
package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/auth"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/model"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/repository"
)

// TestRequireMerchantAuth_ConcurrentRevocationAndRequests proves §23.8
// item 2's revocation half: N goroutines hammering RequireMerchantAuth
// with a valid key while a separate goroutine revokes that SAME key must
// never panic or race (real concurrent GetActiveByPrefix/Revoke DB calls,
// run with -race), every response must be a clean 200 or 401 — nothing
// else — and once revocation has definitely completed, the key must be
// durably and permanently 401 (§8.4: "revocation takes effect
// immediately"; C1 "starts without a positive authentication cache", so
// there is no stale-cache window to tolerate).
func TestRequireMerchantAuth_ConcurrentRevocationAndRequests(t *testing.T) {
	db := setupGatewayTestDB(t)
	ctx := context.Background()

	tenants := repository.NewTenantRepository(db)
	keys := repository.NewAPIKeyRepository(db)
	keySvc := auth.NewKeyService(keys, tenants, integrationTestPepper)

	tenantID := uuid.New()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantID, PublicID: "mrc_" + uuid.NewString()[:16], ExternalCode: "ext-race2-" + tenantID.String(),
		Name: "Race2 Co", Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))
	plaintext, keyID, err := keySvc.CreateKey(ctx, tenantID, "live", []string{"merchant:read"}, "operator")
	require.NoError(t, err)

	handler := auth.RequireMerchantAuth(keys, tenants, integrationTestPepper)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	doAuthedRequest := func() int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/b2b/merchant", nil)
		req.Header.Set("Authorization", "Bearer "+plaintext)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	const concurrency = 30
	var wg sync.WaitGroup
	var successfulRequestCount, unauthorizedRequestCount, unexpected int32
	wg.Add(concurrency + 1)
	for range concurrency {
		go func() {
			defer wg.Done()
			switch doAuthedRequest() {
			case http.StatusOK:
				atomic.AddInt32(&successfulRequestCount, 1)
			case http.StatusUnauthorized:
				atomic.AddInt32(&unauthorizedRequestCount, 1)
			default:
				atomic.AddInt32(&unexpected, 1)
			}
		}()
	}
	go func() {
		defer wg.Done()
		require.NoError(t, keySvc.RevokeKey(ctx, tenantID, keyID, "operator"))
	}()
	wg.Wait()

	assert.Equal(t, int32(0), atomic.LoadInt32(&unexpected), "every concurrent request must resolve to exactly 200 or 401 — no panic, no torn response")
	assert.Equal(t, int32(concurrency), successfulRequestCount+unauthorizedRequestCount, "every goroutine must have gotten a response")

	assert.Equal(t, http.StatusUnauthorized, doAuthedRequest(), "once revocation has completed, the key must be durably unauthorized")
}

// TestKeyService_ConcurrentRotationAndRequests proves §23.8 item 2's
// rotation half: N goroutines using the OLD key concurrently with a
// RotateKey call must never panic or race, and once rotation has
// completed the old key is durably 401 while the new key is durably 200
// — §8.4's "new and old keys may overlap for controlled rotation" is
// about the transition window, not a promise the old key works forever.
func TestKeyService_ConcurrentRotationAndRequests(t *testing.T) {
	db := setupGatewayTestDB(t)
	ctx := context.Background()

	tenants := repository.NewTenantRepository(db)
	keys := repository.NewAPIKeyRepository(db)
	keySvc := auth.NewKeyService(keys, tenants, integrationTestPepper)

	tenantID := uuid.New()
	require.NoError(t, tenants.Create(ctx, model.Tenant{
		ID: tenantID, PublicID: "mrc_" + uuid.NewString()[:16], ExternalCode: "ext-race2b-" + tenantID.String(),
		Name: "Race2b Co", Environment: "live", Status: "active", DefaultCurrency: "IDR", CreatedBy: "test",
	}))
	oldPlaintext, oldKeyID, err := keySvc.CreateKey(ctx, tenantID, "live", []string{"merchant:read"}, "operator")
	require.NoError(t, err)

	handler := auth.RequireMerchantAuth(keys, tenants, integrationTestPepper)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	doAuthedRequest := func(plaintext string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/b2b/merchant", nil)
		req.Header.Set("Authorization", "Bearer "+plaintext)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	const concurrency = 30
	var wg sync.WaitGroup
	var unexpected int32
	var newPlaintext string
	wg.Add(concurrency + 1)
	for range concurrency {
		go func() {
			defer wg.Done()
			code := doAuthedRequest(oldPlaintext)
			if code != http.StatusOK && code != http.StatusUnauthorized {
				atomic.AddInt32(&unexpected, 1)
			}
		}()
	}
	go func() {
		defer wg.Done()
		var rotateErr error
		newPlaintext, _, rotateErr = keySvc.RotateKey(ctx, tenantID, oldKeyID, "live", []string{"merchant:read"}, "operator")
		require.NoError(t, rotateErr)
	}()
	wg.Wait()

	assert.Equal(t, int32(0), atomic.LoadInt32(&unexpected), "every concurrent request against the old key must resolve to exactly 200 or 401")
	require.NotEmpty(t, newPlaintext)

	assert.Equal(t, http.StatusUnauthorized, doAuthedRequest(oldPlaintext), "the old key must be durably revoked once rotation has completed")
	assert.Equal(t, http.StatusOK, doAuthedRequest(newPlaintext), "the new key must work once rotation has completed")
}
