package payout

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

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/payout/repository"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
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

// newAdminTestRouter/newPublicTestRouter mirror internal/payin's own
// http_test.go pattern: run the real pkg/middleware.WithAuth chain (real
// JWT verification) against a Module wired with mocked repo/poster/vendor
// dependencies — this proves the full HTTP-layer vertical (auth ->
// admin-gate/ownership-check -> handler -> business logic) without
// touching Postgres.
func newAdminTestRouter(m *Module) http.Handler {
	return middleware.WithAuth(testSecret, "")(m.AdminRouter())
}

func newPublicTestRouter(m *Module) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /payout", m.CreateHandler())
	mux.Handle("GET /payout/{id}", m.GetHandler())
	return middleware.WithAuth(testSecret, "")(mux)
}

// ─── Admin: list requests ───────────────────────────────────────────────

func TestAdminRouter_ListRequests_NonAdmin_403(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/admin/payout/requests", tokenForUser(t, uuid.New(), "user"), "")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAdminRouter_ListRequests_NoToken_401(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/admin/payout/requests", "", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAdminRouter_ListRequests_Admin_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	repo.EXPECT().List(gomock.Any(), "", "", 50, 0).Return([]model.PayoutRequest{
		sampleRequest(uuid.New(), model.StatusSettled),
	}, nil)
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/admin/payout/requests", tokenForUser(t, uuid.New(), "admin"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "mockvendor")
}

func TestAdminRouter_ListRequests_InvalidLimit_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/admin/payout/requests?limit=-1", tokenForUser(t, uuid.New(), "admin"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ─── Admin: cancel ───────────────────────────────────────────────────────

func TestAdminRouter_Cancel_NonAdmin_403(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/admin/payout/requests/"+uuid.New().String()+"/cancel", tokenForUser(t, uuid.New(), "user"), "")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAdminRouter_Cancel_NotFound_404(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().Get(gomock.Any(), id).Return(model.PayoutRequest{}, repository.ErrNotFound)
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/admin/payout/requests/"+id.String()+"/cancel", tokenForUser(t, uuid.New(), "admin"), "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAdminRouter_Cancel_InvalidTransition_409(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().Get(gomock.Any(), id).Return(sampleRequest(id, model.StatusCreated), nil)
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/admin/payout/requests/"+id.String()+"/cancel", tokenForUser(t, uuid.New(), "admin"), "")
	assert.Equal(t, http.StatusConflict, w.Code, "a request not yet vendor-contacted cannot be admin-cancelled")
}

func TestAdminRouter_Cancel_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	holdTxID := uuid.New()
	cancelTxID := uuid.New()
	req := sampleRequest(id, model.StatusVendorPending)
	req.HoldTxID = &holdTxID

	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil).Times(2)
	repo.EXPECT().TransitionToCancelled(gomock.Any(), id, cancelTxID).Return(true, nil)

	poster := stubPoster{
		postFn: func(_ context.Context, _ ledgerclient.Command) error { return nil },
		getTxFn: func(_ context.Context, _, _ string) (ledgerclient.Transaction, error) {
			return ledgerclient.Transaction{ID: cancelTxID}, nil
		},
	}
	m := newTestModule(repo, poster, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/admin/payout/requests/"+id.String()+"/cancel", tokenForUser(t, uuid.New(), "admin"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"cancelled":true`)
}

// ─── Admin: retry ────────────────────────────────────────────────────────

func TestAdminRouter_Retry_NonAdmin_403(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/admin/payout/requests/"+uuid.New().String()+"/retry", tokenForUser(t, uuid.New(), "user"), "")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAdminRouter_Retry_NotFound_404(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().Get(gomock.Any(), id).Return(model.PayoutRequest{}, repository.ErrNotFound)
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/admin/payout/requests/"+id.String()+"/retry", tokenForUser(t, uuid.New(), "admin"), "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAdminRouter_Retry_InvalidTransition_409(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().Get(gomock.Any(), id).Return(sampleRequest(id, model.StatusHeld), nil)
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/admin/payout/requests/"+id.String()+"/retry", tokenForUser(t, uuid.New(), "admin"), "")
	assert.Equal(t, http.StatusConflict, w.Code, "retry only applies to a request stuck in 'submitted'")
}

func TestAdminRouter_Retry_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	req := sampleRequest(id, model.StatusSubmitted)

	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil).Times(2)
	repo.EXPECT().TransitionToSubmitted(gomock.Any(), id).Return(true, nil)
	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().TransitionToVendorPending(gomock.Any(), id, "vref-retry").Return(true, nil)

	provider := &stubPayoutProvider{
		name: "mockvendor",
		submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
			return vendorgw.PayoutResult{Status: vendorgw.PayoutPending, VendorRef: "vref-retry"}, nil
		},
	}
	m := newTestModule(repo, stubPoster{}, registryWith(provider))
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/admin/payout/requests/"+id.String()+"/retry", tokenForUser(t, uuid.New(), "admin"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"retried":true`)
}

// ─── Public: create ──────────────────────────────────────────────────────

func TestCreateHandler_NoToken_401(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newPublicTestRouter(m)

	body := `{"amount":"1000","vendor":"mockvendor","destination":{"bank_code":"014"}}`
	w := doReq(t, router, http.MethodPost, "/payout", "", body)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCreateHandler_NoRoute_422(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := &Module{repo: repo, poster: stubPoster{}, registry: vendorgw.NewRegistry(), routing: stubRouting{}, logger: discardLogger()}
	router := newPublicTestRouter(m)

	body := `{"amount":"1000","destination":{"bank_code":"014"}}`
	w := doReq(t, router, http.MethodPost, "/payout", tokenForUser(t, uuid.New(), "user"), body)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"NO_ROUTE"`)
}

func TestCreateHandler_ClientVendorRejected_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected — Create rejects before any repo call
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newPublicTestRouter(m)

	body := `{"amount":"1000","vendor":"mockvendor","destination":{"bank_code":"014"}}`
	w := doReq(t, router, http.MethodPost, "/payout", tokenForUser(t, uuid.New(), "user"), body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateHandler_NonIntegralAmount_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newPublicTestRouter(m)

	body := `{"amount":"100.50","destination":{"bank_code":"014"}}`
	w := doReq(t, router, http.MethodPost, "/payout", tokenForUser(t, uuid.New(), "user"), body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateHandler_Success_201(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	holdTxID := uuid.New()
	settleTxID := uuid.New()

	heldReq := sampleRequest(uuid.Nil, model.StatusHeld)
	heldReq.UserID = userID
	heldReq.HoldTxID = &holdTxID

	repo.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().TransitionToHeld(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
	repo.EXPECT().Get(gomock.Any(), gomock.Any()).Return(heldReq, nil).Times(3)
	repo.EXPECT().TransitionToSubmitted(gomock.Any(), gomock.Any()).Return(true, nil)
	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().TransitionToSettled(gomock.Any(), gomock.Any(), settleTxID).Return(true, nil)

	provider := &stubPayoutProvider{
		name: "mockvendor",
		submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
			return vendorgw.PayoutResult{Status: vendorgw.PayoutSettled}, nil
		},
	}
	poster := stubPoster{
		postFn: func(_ context.Context, _ ledgerclient.Command) error { return nil },
		getTxFn: func(_ context.Context, key, _ string) (ledgerclient.Transaction, error) {
			if strings.HasSuffix(key, "hold") {
				return ledgerclient.Transaction{ID: holdTxID}, nil
			}
			return ledgerclient.Transaction{ID: settleTxID}, nil
		},
		getCurrencyFn: func(context.Context, uuid.UUID, string) (string, error) { return "IDR", nil },
	}

	m := newTestModule(repo, poster, registryWith(provider))
	router := newPublicTestRouter(m)

	body := `{"amount":"100000","destination":{"bank_code":"014","account_no":"123"}}`
	w := doReq(t, router, http.MethodPost, "/payout", tokenForUser(t, userID, "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), `"vendor":"mockvendor"`)
}

// ─── Public: get ─────────────────────────────────────────────────────────

func TestGetHandler_NoToken_401(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newPublicTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/payout/"+uuid.New().String(), "", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetHandler_NotFound_404(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().Get(gomock.Any(), id).Return(model.PayoutRequest{}, repository.ErrNotFound)
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newPublicTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/payout/"+id.String(), tokenForUser(t, uuid.New(), "user"), "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGetHandler_OwnershipMismatch_404 is docs/plan/23 Task T5's required
// ownership test: user A must not be able to see user B's payout request —
// reported as 404 (not 403), same "don't confirm existence to a non-owner"
// reasoning as ledger's own CanAccessAccount-based handlers.
func TestGetHandler_OwnershipMismatch_404(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	ownerID := uuid.New()
	otherUserID := uuid.New()
	req := sampleRequest(id, model.StatusSettled)
	req.UserID = ownerID
	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil)
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newPublicTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/payout/"+id.String(), tokenForUser(t, otherUserID, "user"), "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetHandler_Success_200(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	ownerID := uuid.New()
	req := sampleRequest(id, model.StatusSettled)
	req.UserID = ownerID
	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil)
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newPublicTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/payout/"+id.String(), tokenForUser(t, ownerID, "user"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "settled")
}
