//go:build integration

// Proves Plan 59 section 7.4/22.6's maker/checker enforcement, channel
// control, and role gating against a REAL Postgres — none of it existed
// before (repository/templates.go's same-actor rejection had zero test
// coverage). Reuses setupNotifyTestDBs (notify_integration_test.go, same
// package) for the shared ledger+gateway container pair.
package notify_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
	notify "github.com/herdifirdausss/seev/services/gateway/notification"
)

const adminTestJWTSecret = "admin-integration-test-secret"

func adminTokenFor(t *testing.T, userID, role string) string {
	t.Helper()
	token, err := middleware.GenerateToken(adminTestJWTSecret, middleware.Claims{
		UserID: userID, Role: role, Exp: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	return token
}

// envelopeData unwraps the repository's standard {"success":true,"data":{...}}
// response shape (internal/platform/transport/http/response.Envelope) into dst.
func envelopeData(t *testing.T, raw []byte, dst any) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope))
	require.NoError(t, json.Unmarshal(envelope.Data, dst))
}

func adminDo(t *testing.T, handler http.Handler, method, target, userID, role string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Reader
	if body == nil {
		reqBody = bytes.NewReader(nil)
	} else {
		reqBody = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+adminTokenFor(t, userID, role))
	rec := httptest.NewRecorder()
	authed := middleware.WithAuth(adminTestJWTSecret, "")(handler)
	authed.ServeHTTP(rec, req)
	return rec
}

func TestAdminTemplateLifecycle_SameActorApprovalRejected_RealStack(t *testing.T) {
	_, gatewayDB := setupNotifyTestDBs(t)
	module := notify.NewModule(gatewayDB, nil, nil)
	router := module.AdminRouter()

	draftBody, err := json.Marshal(map[string]string{
		"Kind": "money.transfer.sent", "Channel": "email", "Locale": "en-US",
		"SubjectTemplate": "Test subject", "BodyTextTemplate": "Test body",
	})
	require.NoError(t, err)

	makerID := uuid.NewString()
	createRec := adminDo(t, router, http.MethodPost, "/notifications/templates/draft", makerID, "admin_maker", draftBody)
	require.Equal(t, http.StatusCreated, createRec.Code, createRec.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	envelopeData(t, createRec.Body.Bytes(), &created)
	require.NotEmpty(t, created.ID)

	submitRec := adminDo(t, router, http.MethodPost, "/notifications/templates/"+created.ID+"/submit", makerID, "admin_maker", nil)
	require.Equal(t, http.StatusNoContent, submitRec.Code, submitRec.Body.String())

	// The maker attempting to approve their own draft — even carrying a
	// checker role claim — must be rejected: repository/templates.go enforces
	// created_by <> actor at the SQL layer, independent of what role the
	// caller's token happens to assert.
	sameActorApprove := adminDo(t, router, http.MethodPost, "/notifications/templates/"+created.ID+"/approve", makerID, "admin_checker", nil)
	require.Equal(t, http.StatusConflict, sameActorApprove.Code, "same actor must not approve their own draft")

	checkerID := uuid.NewString()
	approveRec := adminDo(t, router, http.MethodPost, "/notifications/templates/"+created.ID+"/approve", checkerID, "admin_checker", nil)
	require.Equal(t, http.StatusNoContent, approveRec.Code, approveRec.Body.String())
}

func TestAdminTemplateLifecycle_RejectRequiresCheckerRoleAndReason_RealStack(t *testing.T) {
	_, gatewayDB := setupNotifyTestDBs(t)
	module := notify.NewModule(gatewayDB, nil, nil)
	router := module.AdminRouter()

	draftBody, err := json.Marshal(map[string]string{
		"Kind": "money.transfer.sent", "Channel": "email", "Locale": "en-US",
		"SubjectTemplate": "Test subject", "BodyTextTemplate": "Test body",
	})
	require.NoError(t, err)
	makerID := uuid.NewString()
	createRec := adminDo(t, router, http.MethodPost, "/notifications/templates/draft", makerID, "admin_maker", draftBody)
	require.Equal(t, http.StatusCreated, createRec.Code, createRec.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	envelopeData(t, createRec.Body.Bytes(), &created)
	require.Equal(t, http.StatusNoContent, adminDo(t, router, http.MethodPost, "/notifications/templates/"+created.ID+"/submit", makerID, "admin_maker", nil).Code)

	// A maker (not checker) attempting to reject must be forbidden by the
	// handler's own role gate, independent of the outer Gateway router's
	// middleware.WithRole chain (docs Plan 59 section 22.6: "Gateway
	// re-validates security-sensitive rules").
	forbidden := adminDo(t, router, http.MethodPost, "/notifications/templates/"+created.ID+"/reject", makerID, "admin_maker", []byte(`{"reason":"typo"}`))
	require.Equal(t, http.StatusForbidden, forbidden.Code)

	checkerID := uuid.NewString()
	rejectRec := adminDo(t, router, http.MethodPost, "/notifications/templates/"+created.ID+"/reject", checkerID, "admin_checker", []byte(`{"reason":"copy error"}`))
	require.Equal(t, http.StatusNoContent, rejectRec.Code, rejectRec.Body.String())
}

func TestAdminChannelControl_RoundTripsThroughRealStack(t *testing.T) {
	_, gatewayDB := setupNotifyTestDBs(t)
	module := notify.NewModule(gatewayDB, nil, nil)
	router := module.AdminRouter()

	operatorID := uuid.NewString()
	setRec := adminDo(t, router, http.MethodPut, "/notifications/channels/email", operatorID, "admin_maker", []byte(`{"state":"paused","reason":"provider maintenance"}`))
	require.Equal(t, http.StatusNoContent, setRec.Code, setRec.Body.String())

	getRec := adminDo(t, router, http.MethodGet, "/notifications/channels/email", operatorID, "admin_maker", nil)
	require.Equal(t, http.StatusOK, getRec.Code)
	var control struct {
		Channel string `json:"channel"`
		State   string `json:"state"`
	}
	envelopeData(t, getRec.Body.Bytes(), &control)
	require.Equal(t, "paused", control.State)

	// A read-only role (no admin/maker/checker claim) must not be able to
	// mutate channel state.
	readOnlyRec := adminDo(t, router, http.MethodPut, "/notifications/channels/email", uuid.NewString(), "user", []byte(`{"state":"running"}`))
	require.Equal(t, http.StatusForbidden, readOnlyRec.Code)
}

func TestAdminReplay_UnknownDeliveryReturnsNotFound_RealStack(t *testing.T) {
	_, gatewayDB := setupNotifyTestDBs(t)
	module := notify.NewModule(gatewayDB, nil, nil)
	router := module.AdminRouter()

	operatorID := uuid.NewString()
	rec := adminDo(t, router, http.MethodPost, "/notifications/deliveries/"+uuid.NewString()+"/replay", operatorID, "admin_maker", []byte(`{"reason":"retry after outage"}`))
	require.Equal(t, http.StatusNotFound, rec.Code)
}
