package payin

import (
	"context"
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
