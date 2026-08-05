package adminbff

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/config"
	"github.com/herdifirdausss/seev/services/adminbff/internal/client"
)

// TestProxyFormBodySurvivesCSRFParse is the regression test for the body-drain
// hazard: RequireCSRF calls r.ParseForm() for hidden-field CSRF submissions
// (all plain <form> POSTs from the operator console), which drains r.Body via
// io.ReadAll internally. The generic proxy() handler previously did its own
// io.ReadAll and therefore read an empty body, silently forwarding {} downstream.
// After the fix, proxy() uses r.ParseForm()/r.PostForm (idempotent, already
// cached) so all form fields arrive at the downstream service intact.
func TestProxyFormBodySurvivesCSRFParse(t *testing.T) {
	var capturedBody []byte
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer downstream.Close()

	const sessionID = "sess-regression"
	const csrfToken = "csrf-regression"
	fake := &sessionRepoFake{sessions: map[string]Session{
		sessionID: {
			ID: sessionID, UserID: uuid.New(), Email: "operator@example.test", Role: "admin",
			CSRFToken: csrfToken, ExpiresAt: time.Now().Add(time.Hour), AbsoluteExpiresAt: time.Now().Add(2 * time.Hour),
		},
	}}
	m := &Module{
		repo:  fake,
		audit: &auditRecorderFake{},
		cfg: config.AdminBFFConfig{
			JWTSecret: "test-regression-secret-32charlong!", JWTIssuer: "test",
			DownstreamTokenTTL: time.Minute, SecureCookie: false, SessionIdleTTL: time.Minute,
		},
		clients: client.Clients{
			Ledger: client.New("ledger", downstream.URL, downstream.Client()),
		},
	}

	formBody := url.Values{
		"type":       {"manual"},
		"user_id":    {uuid.New().String()},
		"amount":     {"50000"},
		"reason":     {"regression test"},
		"csrf_token": {csrfToken},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/adjustments",
		strings.NewReader(formBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})

	handler := m.RequireSession(m.RequireCSRF(
		m.proxy("ledger", m.clients.Ledger, "/api/v1/admin/adjustments", "/api/v1/ledger/admin/adjustments"),
	))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "downstream: %s", rec.Body.String())
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(capturedBody, &decoded), "downstream received: %q", string(capturedBody))
	require.Equal(t, "manual", decoded["type"], "type field must survive CSRF body drain")
	require.Equal(t, "50000", decoded["amount"], "amount field must survive CSRF body drain")
	require.Equal(t, "regression test", decoded["reason"], "reason field must survive CSRF body drain")
	require.NotContains(t, decoded, "csrf_token", "CSRF token must never be forwarded downstream")
}

// TestProxyJSONBodyPassedThrough verifies that a non-form POST body is still
// forwarded unchanged — the new form-encoding branch must not affect JSON.
func TestProxyJSONBodyPassedThrough(t *testing.T) {
	var capturedBody []byte
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer downstream.Close()

	const sessionID = "sess-json"
	const csrfToken = "csrf-json"
	fake := &sessionRepoFake{sessions: map[string]Session{
		sessionID: {
			ID: sessionID, UserID: uuid.New(), Email: "operator@example.test", Role: "admin",
			CSRFToken: csrfToken, ExpiresAt: time.Now().Add(time.Hour), AbsoluteExpiresAt: time.Now().Add(2 * time.Hour),
		},
	}}
	m := &Module{
		repo:  fake,
		audit: &auditRecorderFake{},
		cfg: config.AdminBFFConfig{
			JWTSecret: "test-regression-secret-32charlong!", JWTIssuer: "test",
			DownstreamTokenTTL: time.Minute, SecureCookie: false, SessionIdleTTL: time.Minute,
		},
		clients: client.Clients{
			Ledger: client.New("ledger", downstream.URL, downstream.Client()),
		},
	}

	jsonPayload := `{"key":"value","count":42}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/adjustments", strings.NewReader(jsonPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfToken) // JSON callers supply CSRF via header, not body
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})

	handler := m.RequireSession(m.RequireCSRF(
		m.proxy("ledger", m.clients.Ledger, "/api/v1/admin/adjustments", "/api/v1/ledger/admin/adjustments"),
	))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, jsonPayload, string(capturedBody), "JSON body must be forwarded unchanged")
}

// TestRewriteProxyPath_LedgerAdminCatchAll proves the routing-bug fix (a
// load-testing session's finding): "/api/v1/admin/ledger/*" must rewrite to
// "/api/v1/ledger/admin/*" — the one downstream mount
// (services/ledger/cmd/ledger/main.go) that actually strips cleanly to
// ledger-service's own "/admin/*" route table. Before this fix,
// downstreamPrefix equaled publicPrefix (no rewrite at all), so the request
// path reached ledger-service unchanged and hit a mount with no possible
// correct StripPrefix length — every disbursement/savings/schedule request
// through this catch-all 404d.
func TestRewriteProxyPath_LedgerAdminCatchAll(t *testing.T) {
	got := rewriteProxyPath("/api/v1/admin/ledger/disbursements", "", "/api/v1/admin/ledger/", "/api/v1/ledger/admin/")
	want := "/api/v1/ledger/admin/disbursements"
	if got != want {
		t.Fatalf("rewriteProxyPath() = %q, want %q", got, want)
	}
}

func TestRewriteProxyPath_PreservesQueryString(t *testing.T) {
	got := rewriteProxyPath("/api/v1/admin/ledger/disbursements/abc/run", "retry_failed=true", "/api/v1/admin/ledger/", "/api/v1/ledger/admin/")
	want := "/api/v1/ledger/admin/disbursements/abc/run?retry_failed=true"
	if got != want {
		t.Fatalf("rewriteProxyPath() = %q, want %q", got, want)
	}
}

// TestRewriteProxyPath_AdjustmentsAndRecon proves the two routes this
// session's fix was modeled on keep working unchanged.
func TestRewriteProxyPath_AdjustmentsAndRecon(t *testing.T) {
	cases := []struct {
		name             string
		requestPath      string
		publicPrefix     string
		downstreamPrefix string
		want             string
	}{
		{"adjustments", "/api/v1/admin/adjustments/xyz", "/api/v1/admin/adjustments/", "/api/v1/ledger/admin/adjustments/", "/api/v1/ledger/admin/adjustments/xyz"},
		{"recon", "/api/v1/admin/recon/batches/1", "/api/v1/admin/recon/", "/api/v1/ledger/admin/recon/", "/api/v1/ledger/admin/recon/batches/1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteProxyPath(tc.requestPath, "", tc.publicPrefix, tc.downstreamPrefix)
			if got != tc.want {
				t.Fatalf("rewriteProxyPath() = %q, want %q", got, tc.want)
			}
		})
	}
}
