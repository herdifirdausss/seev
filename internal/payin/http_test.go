package payin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/internal/payin/repository"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

const testSecret = "supersecretkeythatisatleast32chars!"

func newAdminTestRouter(t *testing.T, m *Module) http.Handler {
	t.Helper()
	return middleware.WithAuth(testSecret, "")(m.AdminRouter())
}

func tokenFor(t *testing.T, role string) string {
	t.Helper()
	tok, err := middleware.GenerateToken(testSecret, middleware.Claims{
		UserID: uuid.New().String(), Role: role, Exp: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	return tok
}

func doAdminReq(t *testing.T, h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestAdminRouter_ListEvents_NonAdmin_403(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := &Module{repo: repo}
	router := newAdminTestRouter(t, m)

	w := doAdminReq(t, router, http.MethodGet, "/admin/payin/events", tokenFor(t, "user"))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAdminRouter_ListEvents_NoToken_401(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	m := &Module{repo: repo}
	router := newAdminTestRouter(t, m)

	w := doAdminReq(t, router, http.MethodGet, "/admin/payin/events", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAdminRouter_ListEvents_Admin_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	repo.EXPECT().List(gomock.Any(), "", "", 50, 0).Return([]model.WebhookEvent{
		{ID: uuid.New(), Vendor: "mockvendor", Status: "posted", Amount: decimal.NewFromInt(1000)},
	}, nil)
	m := &Module{repo: repo}
	router := newAdminTestRouter(t, m)

	w := doAdminReq(t, router, http.MethodGet, "/admin/payin/events", tokenFor(t, "admin"))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "mockvendor")
}

func TestAdminRouter_ListEvents_InvalidLimit_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := &Module{repo: repo}
	router := newAdminTestRouter(t, m)

	w := doAdminReq(t, router, http.MethodGet, "/admin/payin/events?limit=-1", tokenFor(t, "admin"))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAdminRouter_ReplayEvent_NonAdmin_403(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := &Module{repo: repo}
	router := newAdminTestRouter(t, m)

	w := doAdminReq(t, router, http.MethodPost, "/admin/payin/events/"+uuid.New().String()+"/replay", tokenFor(t, "user"))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAdminRouter_ReplayEvent_NotFound_404(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().Get(gomock.Any(), id).Return(model.WebhookEvent{}, repository.ErrNotFound)
	m := &Module{repo: repo}
	router := newAdminTestRouter(t, m)

	w := doAdminReq(t, router, http.MethodPost, "/admin/payin/events/"+id.String()+"/replay", tokenFor(t, "admin"))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAdminRouter_ReplayEvent_AlreadyPosted_409(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().Get(gomock.Any(), id).Return(model.WebhookEvent{ID: id, Status: "posted"}, nil)
	m := &Module{repo: repo}
	router := newAdminTestRouter(t, m)

	w := doAdminReq(t, router, http.MethodPost, "/admin/payin/events/"+id.String()+"/replay", tokenFor(t, "admin"))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestAdminRouter_ReplayEvent_FailedEvent_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().Get(gomock.Any(), id).Return(model.WebhookEvent{
		ID: id, Vendor: "mockvendor", VendorEventID: "evt-1", ExternalRef: "ref-1",
		UserID: uuid.New(), Amount: decimal.NewFromInt(5000), Currency: "IDR", Status: "failed",
	}, nil)
	repo.EXPECT().MarkPosted(gomock.Any(), id).Return(nil)
	repo.EXPECT().MarkTopupIntentSettled(gomock.Any(), "ref-1", id).Return(false, nil)

	postCalls := 0
	poster := stubPoster{fn: func(_ context.Context, _ ledgerclient.Command) error {
		postCalls++
		return nil
	}}
	m := &Module{repo: repo, poster: poster, routing: routeTo("mockvendor", "bca"), logger: discardLogger()}
	router := newAdminTestRouter(t, m)

	w := doAdminReq(t, router, http.MethodPost, "/admin/payin/events/"+id.String()+"/replay", tokenFor(t, "admin"))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, postCalls)
}

// ─── Admin: vendor health (docs/plan/40 Task T5) ────────────────────────

func TestAdminRouter_VendorHealth_NonAdmin_403(t *testing.T) {
	m := &Module{}
	router := newAdminTestRouter(t, m)

	w := doAdminReq(t, router, http.MethodGet, "/admin/payin/vendors/health", tokenFor(t, "user"))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAdminRouter_VendorHealth_NilBreaker_EmptyList(t *testing.T) {
	m := &Module{} // breaker deliberately nil
	router := newAdminTestRouter(t, m)

	w := doAdminReq(t, router, http.MethodGet, "/admin/payin/vendors/health", tokenFor(t, "admin"))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"vendors":[]`)
}

// TestAdminRouter_VendorHealth_ReportsAllThreeStates is docs/plan/40 Task
// T5's own required test: a tracker seeded with closed, open, AND
// half-open vendors must report each state accurately in one snapshot.
func TestAdminRouter_VendorHealth_ReportsAllThreeStates(t *testing.T) {
	breaker := vendorgw.NewHealthTracker(1, time.Nanosecond, nil)
	breaker.RecordFailure(context.Background(), "open-vendor")
	breaker.RecordFailure(context.Background(), "half-open-vendor")
	assert.True(t, breaker.Allow(context.Background(), "half-open-vendor"), "cooldown of 1ns has elapsed by the time Allow is called, promoting to half-open")
	// "closed-vendor" is never touched — stays closed by default, and
	// therefore has no entry in Snapshot() at all.

	m := &Module{breaker: breaker}
	router := newAdminTestRouter(t, m)

	w := doAdminReq(t, router, http.MethodGet, "/admin/payin/vendors/health", tokenFor(t, "admin"))
	assert.Equal(t, http.StatusOK, w.Code)

	var envelope struct {
		Data struct {
			Vendors []vendorgw.VendorHealth `json:"vendors"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	states := make(map[string]vendorgw.HealthState, len(envelope.Data.Vendors))
	for _, v := range envelope.Data.Vendors {
		states[v.Vendor] = v.State
	}
	assert.Equal(t, vendorgw.StateOpen, states["open-vendor"])
	assert.Equal(t, vendorgw.StateHalfOpen, states["half-open-vendor"])
	assert.Empty(t, states["closed-vendor"])
}
