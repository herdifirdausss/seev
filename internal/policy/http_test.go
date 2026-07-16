package policy

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

	"github.com/herdifirdausss/seev/pkg/middleware"
)

const testSecret = "supersecretkeythatisatleast32chars!"

func newTestHandler(t *testing.T) (http.Handler, *MockRepository, *gomock.Controller) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	h := middleware.WithAuth(testSecret, "")(NewHandler(repo).Mux())
	return h, repo, ctrl
}

func tokenFor(t *testing.T, userID, role string) string {
	t.Helper()
	tok, err := middleware.GenerateToken(testSecret, middleware.Claims{
		UserID: userID, Role: role, Exp: time.Now().Add(time.Hour).Unix(),
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

func TestUpsertLimit_RejectedForNonAdmin(t *testing.T) {
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()

	body := `{"transaction_type":"transfer_p2p","max_per_tx":"5000","enabled":true}`
	w := doReq(t, h, http.MethodPut, "/admin/policy/limits", tokenFor(t, uuid.NewString(), "user"), body)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestUpsertLimit_MissingTransactionType_Rejected(t *testing.T) {
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()

	body := `{"max_per_tx":"5000","enabled":true}`
	w := doReq(t, h, http.MethodPut, "/admin/policy/limits", tokenFor(t, uuid.NewString(), "admin"), body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpsertLimit_NonIntegralAmount_Rejected(t *testing.T) {
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()

	body := `{"transaction_type":"transfer_p2p","max_per_tx":"5000.50","enabled":true}`
	w := doReq(t, h, http.MethodPut, "/admin/policy/limits", tokenFor(t, uuid.NewString(), "admin"), body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "max_per_tx")
}

func TestUpsertLimit_InvalidUserID_Rejected(t *testing.T) {
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()

	body := `{"user_id":"not-a-uuid","transaction_type":"transfer_p2p","enabled":true}`
	w := doReq(t, h, http.MethodPut, "/admin/policy/limits", tokenFor(t, uuid.NewString(), "admin"), body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpsertLimit_Valid_Succeeds(t *testing.T) {
	h, repo, ctrl := newTestHandler(t)
	defer ctrl.Finish()

	repo.EXPECT().Upsert(gomock.Any(), gomock.Any()).DoAndReturn(func(_ interface{}, l Limit) (Limit, error) {
		l.ID = uuid.New()
		return l, nil
	})

	body := `{"transaction_type":"transfer_p2p","max_per_tx":"5000","max_daily_amount":"20000","enabled":true}`
	w := doReq(t, h, http.MethodPut, "/admin/policy/limits", tokenFor(t, uuid.NewString(), "admin"), body)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"max_per_tx":"5000"`)
	assert.Contains(t, w.Body.String(), `"max_daily_amount":"20000"`)
}

func TestUpsertLimit_UserSpecific_Succeeds(t *testing.T) {
	h, repo, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	repo.EXPECT().Upsert(gomock.Any(), gomock.Any()).DoAndReturn(func(_ interface{}, l Limit) (Limit, error) {
		require.NotNil(t, l.UserID)
		assert.Equal(t, userID, *l.UserID)
		l.ID = uuid.New()
		return l, nil
	})

	body := `{"user_id":"` + userID.String() + `","transaction_type":"transfer_p2p","max_per_tx":"500","enabled":true}`
	w := doReq(t, h, http.MethodPut, "/admin/policy/limits", tokenFor(t, uuid.NewString(), "admin"), body)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListLimits_RejectedForNonAdmin(t *testing.T) {
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()

	w := doReq(t, h, http.MethodGet, "/admin/policy/limits", tokenFor(t, uuid.NewString(), "user"), "")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestListLimits_ReturnsRepositoryResults(t *testing.T) {
	h, repo, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	perTx := int64(1000)

	repo.EXPECT().List(gomock.Any(), "transfer_p2p", (*uuid.UUID)(nil)).Return([]Limit{
		{ID: uuid.New(), TransactionType: "transfer_p2p", MaxPerTx: &perTx, Enabled: true},
	}, nil)

	w := doReq(t, h, http.MethodGet, "/admin/policy/limits?type=transfer_p2p", tokenFor(t, uuid.NewString(), "admin"), "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"transaction_type":"transfer_p2p"`)
}

func TestListLimits_InvalidUserIDFilter_Rejected(t *testing.T) {
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()

	w := doReq(t, h, http.MethodGet, "/admin/policy/limits?user_id=not-a-uuid", tokenFor(t, uuid.NewString(), "admin"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
