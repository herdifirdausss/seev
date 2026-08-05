package adminbff

import (
	"context"
	"encoding/json"
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

type auditRecorderFake struct{ entries []AuditEntry }

func (f *auditRecorderFake) WriteAudit(_ context.Context, entry AuditEntry) error {
	f.entries = append(f.entries, entry)
	return nil
}

func newNotificationProxyTestModule(t *testing.T, downstream *httptest.Server) (*Module, *auditRecorderFake) {
	t.Helper()
	audit := &auditRecorderFake{}
	m := &Module{
		clients: client.Clients{Gateway: client.New("gateway", downstream.URL, downstream.Client())},
		cfg:     config.AdminBFFConfig{JWTSecret: "test-secret", JWTIssuer: "adminbff-test", DownstreamTokenTTL: time.Minute},
		audit:   audit,
	}
	return m, audit
}

// withOperatorSession simulates what RequireSession already put in context by
// the time a route handler runs, and — critically — calls r.ParseForm() the
// same way RequireCSRF does for every real <form> POST that carries its CSRF
// token as a hidden field instead of a header. If a handler under test read
// the raw body again afterward instead of going through r.FormValue, this
// would reproduce the drained-body hazard and the test would see empty
// fields reach the downstream fake.
func withOperatorSession(r *http.Request, role string) *http.Request {
	session := &Session{ID: "s1", UserID: uuid.New(), Email: "operator@example.test", Role: role, CSRFToken: "csrf"}
	ctx := context.WithValue(r.Context(), adminSessionKey, session)
	r = r.WithContext(ctx)
	_ = r.ParseForm()
	return r
}

func formRequest(t *testing.T, method, target string, fields url.Values) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(fields.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestNotificationTemplateDraftProxy_FormFieldsSurviveCSRFParse(t *testing.T) {
	var capturedPath, capturedMethod string
	var capturedBody map[string]any
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath, capturedMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"` + uuid.NewString() + `"}`))
	}))
	defer downstream.Close()

	m, audit := newNotificationProxyTestModule(t, downstream)
	fields := url.Values{
		"kind": {"money.transfer.sent"}, "channel": {"email"}, "locale": {"en-US"},
		"subject_template": {"You sent money"}, "body_text_template": {"Sent."}, "csrf_token": {"csrf"},
	}
	req := withOperatorSession(formRequest(t, http.MethodPost, "/api/v1/admin/notifications/templates/draft", fields), "admin_maker")
	rec := httptest.NewRecorder()

	m.notificationTemplateDraftProxy().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, http.MethodPost, capturedMethod)
	require.Equal(t, "/api/v1/admin/gateway/notifications/templates/draft", capturedPath)
	require.Equal(t, "money.transfer.sent", capturedBody["Kind"])
	require.Equal(t, "email", capturedBody["Channel"])
	require.Equal(t, "You sent money", capturedBody["SubjectTemplate"])
	require.Len(t, audit.entries, 1)
	require.Equal(t, "gateway", audit.entries[0].TargetService)
	require.Equal(t, "notification_template_draft", audit.entries[0].Summary["operation"])
}

func TestNotificationTemplateDecisionProxy_ApproveBuildsIDPath(t *testing.T) {
	id := uuid.New()
	var capturedPath, capturedMethod string
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath, capturedMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer downstream.Close()

	m, audit := newNotificationProxyTestModule(t, downstream)
	fields := url.Values{"id": {id.String()}, "csrf_token": {"csrf"}}
	req := withOperatorSession(formRequest(t, http.MethodPost, "/api/v1/admin/notifications/templates/approve", fields), "admin_checker")
	rec := httptest.NewRecorder()

	m.notificationTemplateDecisionProxy("approve").ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, http.MethodPost, capturedMethod)
	require.Equal(t, "/api/v1/admin/gateway/notifications/templates/"+id.String()+"/approve", capturedPath)
	require.Len(t, audit.entries, 1)
	require.Equal(t, "notification_template_approve", audit.entries[0].Summary["operation"])
}

func TestNotificationTemplateDecisionProxy_RejectForwardsReason(t *testing.T) {
	id := uuid.New()
	var capturedBody map[string]string
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer downstream.Close()

	m, _ := newNotificationProxyTestModule(t, downstream)
	fields := url.Values{"id": {id.String()}, "reason": {"copy error"}, "csrf_token": {"csrf"}}
	req := withOperatorSession(formRequest(t, http.MethodPost, "/api/v1/admin/notifications/templates/reject", fields), "admin_checker")
	rec := httptest.NewRecorder()

	m.notificationTemplateDecisionProxy("reject").ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "copy error", capturedBody["reason"])
}

func TestNotificationTemplateDecisionProxy_InvalidIDRejected(t *testing.T) {
	m, _ := newNotificationProxyTestModule(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream should not be called for an invalid id")
	})))
	fields := url.Values{"id": {"not-a-uuid"}, "csrf_token": {"csrf"}}
	req := withOperatorSession(formRequest(t, http.MethodPost, "/api/v1/admin/notifications/templates/approve", fields), "admin_checker")
	rec := httptest.NewRecorder()

	m.notificationTemplateDecisionProxy("approve").ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNotificationDeliveryReplayProxy_RequiresReason(t *testing.T) {
	m, _ := newNotificationProxyTestModule(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream should not be called without a reason")
	})))
	fields := url.Values{"id": {uuid.NewString()}, "csrf_token": {"csrf"}}
	req := withOperatorSession(formRequest(t, http.MethodPost, "/api/v1/admin/notifications/deliveries/replay", fields), "admin_maker")
	rec := httptest.NewRecorder()

	m.notificationDeliveryReplayProxy().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNotificationDeliveryReplayProxy_ForwardsReplayPath(t *testing.T) {
	id := uuid.New()
	var capturedPath, capturedMethod string
	var capturedBody map[string]string
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath, capturedMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer downstream.Close()

	m, audit := newNotificationProxyTestModule(t, downstream)
	fields := url.Values{"id": {id.String()}, "reason": {"provider outage recovered"}, "csrf_token": {"csrf"}}
	req := withOperatorSession(formRequest(t, http.MethodPost, "/api/v1/admin/notifications/deliveries/replay", fields), "admin_maker")
	rec := httptest.NewRecorder()

	m.notificationDeliveryReplayProxy().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, http.MethodPost, capturedMethod)
	require.Equal(t, "/api/v1/admin/gateway/notifications/deliveries/"+id.String()+"/replay", capturedPath)
	require.Equal(t, "provider outage recovered", capturedBody["reason"])
	require.Equal(t, "notification_delivery_replay", audit.entries[0].Summary["operation"])
}

func TestNotificationChannelControlProxy_RejectsUnknownChannel(t *testing.T) {
	m, _ := newNotificationProxyTestModule(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream should not be called for an unknown channel")
	})))
	fields := url.Values{"channel": {"sms"}, "reason": {"test"}, "csrf_token": {"csrf"}}
	req := withOperatorSession(formRequest(t, http.MethodPost, "/api/v1/admin/notifications/channels/pause", fields), "admin_maker")
	rec := httptest.NewRecorder()

	m.notificationChannelControlProxy("paused").ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNotificationChannelControlProxy_PauseSendsPUTWithState(t *testing.T) {
	var capturedPath, capturedMethod string
	var capturedBody map[string]string
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath, capturedMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer downstream.Close()

	m, audit := newNotificationProxyTestModule(t, downstream)
	fields := url.Values{"channel": {"email"}, "reason": {"provider maintenance"}, "csrf_token": {"csrf"}}
	req := withOperatorSession(formRequest(t, http.MethodPost, "/api/v1/admin/notifications/channels/pause", fields), "admin_maker")
	rec := httptest.NewRecorder()

	m.notificationChannelControlProxy("paused").ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, http.MethodPut, capturedMethod)
	require.Equal(t, "/api/v1/admin/gateway/notifications/channels/email", capturedPath)
	require.Equal(t, "paused", capturedBody["state"])
	require.Equal(t, "provider maintenance", capturedBody["reason"])
	require.Equal(t, "notification_channel_paused", audit.entries[0].Summary["operation"])
}

func TestNotificationDeliveryDetailProxy_ForwardsGET(t *testing.T) {
	id := uuid.New()
	var capturedPath, capturedMethod string
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath, capturedMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"` + id.String() + `"}`))
	}))
	defer downstream.Close()

	m, _ := newNotificationProxyTestModule(t, downstream)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/notifications/deliveries/detail?id="+id.String(), nil)
	session := &Session{ID: "s1", UserID: uuid.New(), Email: "operator@example.test", Role: "admin_checker", CSRFToken: "csrf"}
	req = req.WithContext(context.WithValue(req.Context(), adminSessionKey, session))
	rec := httptest.NewRecorder()

	m.notificationDeliveryDetailProxy().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, http.MethodGet, capturedMethod)
	require.Equal(t, "/api/v1/admin/gateway/notifications/deliveries/"+id.String(), capturedPath)
}
