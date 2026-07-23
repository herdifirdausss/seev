package notify

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/internal/notify/model"
	"github.com/herdifirdausss/seev/internal/notify/repository"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

const testSecret = "supersecretkeythatisatleast32chars!"

func tokenForUser(t *testing.T, userID uuid.UUID, role string) string {
	t.Helper()
	tok, err := middleware.GenerateToken(testSecret, middleware.Claims{
		UserID: userID.String(), Role: role, Exp: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	return tok
}

func doReq(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func newPublicTestRouter(m *Module) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /notifications", m.ListHandler())
	mux.Handle("POST /notifications/{id}/read", m.MarkReadHandler())
	return middleware.WithAuth(testSecret, "")(mux)
}

func TestListHandler_NoToken_401(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := &Module{repo: repo, logger: discardLogger()}
	router := newPublicTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/notifications", "", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListHandler_InvalidLimit_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := &Module{repo: repo, logger: discardLogger()}
	router := newPublicTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/notifications?limit=-1", tokenForUser(t, uuid.New(), "user"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListHandler_InvalidBefore_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := &Module{repo: repo, logger: discardLogger()}
	router := newPublicTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/notifications?before=not-a-time", tokenForUser(t, uuid.New(), "user"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListHandler_Success_200_OwnRowsOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	repo.EXPECT().List(gomock.Any(), userID, 50, time.Time{}).Return([]model.Notification{
		{ID: uuid.New(), UserID: userID, Type: "money_in", Title: "Funds received", Body: "Top-up successful", CreatedAt: time.Now()},
	}, nil)

	m := &Module{repo: repo, logger: discardLogger()}
	router := newPublicTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/notifications", tokenForUser(t, userID, "user"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Funds received")
}

func TestMarkReadHandler_NoToken_401(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := &Module{repo: repo, logger: discardLogger()}
	router := newPublicTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/notifications/"+uuid.New().String()+"/read", "", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestMarkReadHandler_InvalidID_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := &Module{repo: repo, logger: discardLogger()}
	router := newPublicTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/notifications/not-a-uuid/read", tokenForUser(t, uuid.New(), "user"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestMarkReadHandler_OwnershipMismatch_404 proves a different user's
// notification id is reported 404 — ownership is enforced by the
// repository's conditional UPDATE (WHERE id=$1 AND user_id=$2), which
// MarkRead surfaces as matched=false, mapped here to 404, never confirming
// existence to a non-owner (same reasoning as internal/payin/payout's own
// ownership tests).
func TestMarkReadHandler_OwnershipMismatch_404(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	otherUserID := uuid.New()
	repo.EXPECT().MarkRead(gomock.Any(), id, otherUserID).Return(false, nil)

	m := &Module{repo: repo, logger: discardLogger()}
	router := newPublicTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/notifications/"+id.String()+"/read", tokenForUser(t, otherUserID, "user"), "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMarkReadHandler_Success_204(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	userID := uuid.New()
	repo.EXPECT().MarkRead(gomock.Any(), id, userID).Return(true, nil)

	m := &Module{repo: repo, logger: discardLogger()}
	router := newPublicTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/notifications/"+id.String()+"/read", tokenForUser(t, userID, "user"), "")
	assert.Equal(t, http.StatusNoContent, w.Code)
}
