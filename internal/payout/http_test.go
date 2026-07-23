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
	"github.com/herdifirdausss/seev/internal/vendorgw/mockvendor"
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
	cmdRepo := repository.NewMockVendorCommandRepository(ctrl)
	id := uuid.New()
	req := sampleRequest(id, model.StatusSubmitted)

	// docs/roadmap/archive/45 Task T1: AdminRetry only ensures a command exists — it
	// never calls the vendor itself; the relay dispatches whatever it
	// enqueues.
	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil)
	cmdRepo.EXPECT().GetLiveCommand(gomock.Any(), id).Return(model.PayoutVendorCommand{}, false, nil)
	cmdRepo.EXPECT().EnsureSubmitCommand(gomock.Any(), id, req.Vendor).Return(true, nil)

	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	m.commandRepo = cmdRepo
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
	cmdRepo := repository.NewMockVendorCommandRepository(ctrl)
	userID := uuid.New()
	holdTxID := uuid.New()

	// docs/roadmap/archive/45 Task T1: Create returns after hold+enqueue, without ever
	// calling the vendor — dispatch is the relay's job alone.
	heldReq := sampleRequest(uuid.Nil, model.StatusSubmitted)
	heldReq.UserID = userID

	repo.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().TransitionToHeld(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
	cmdRepo.EXPECT().EnqueueInitialSubmit(gomock.Any(), gomock.Any(), "mockvendor").Return(true, nil)
	// CreateHandler re-fetches the row to build the JSON response.
	repo.EXPECT().Get(gomock.Any(), gomock.Any()).Return(heldReq, nil)

	provider := &stubPayoutProvider{
		name: "mockvendor",
		submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
			t.Fatal("Create must never call provider.Submit directly")
			return vendorgw.PayoutResult{}, nil
		},
	}
	poster := stubPoster{
		postFn: func(_ context.Context, _ ledgerclient.Command) error { return nil },
		getTxFn: func(_ context.Context, key, _ string) (ledgerclient.Transaction, error) {
			require.True(t, strings.HasSuffix(key, "hold"))
			return ledgerclient.Transaction{ID: holdTxID}, nil
		},
		getCurrencyFn: func(context.Context, uuid.UUID, string) (string, error) { return "IDR", nil },
	}

	m := newTestModule(repo, poster, registryWith(provider))
	m.commandRepo = cmdRepo
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

// TestGetHandler_OwnershipMismatch_404 is docs/roadmap/archive/23 Task T5's required
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

// ─── Admin: vendor force-fail (docs/roadmap/archive/40 Task T4) ────────────────────

func TestAdminRouter_ForceFail_NonAdmin_403(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/admin/payout/vendors/mockvendor/force-fail", tokenForUser(t, uuid.New(), "user"), `{"fail":true}`)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAdminRouter_ForceFail_UnregisteredVendor_404(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/admin/payout/vendors/nosuchvendor/force-fail", tokenForUser(t, uuid.New(), "admin"), `{"fail":true}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// stubPayoutProviderNoForceFail deliberately does NOT implement
// forceFailSwitch, proving a vendor without the capability reports 400
// rather than silently no-op-ing.
type stubPayoutProviderNoForceFail struct{ *stubPayoutProvider }

func TestAdminRouter_ForceFail_VendorWithoutSwitch_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	provider := &stubPayoutProviderNoForceFail{&stubPayoutProvider{name: "plainvendor"}}
	m := newTestModule(repo, stubPoster{}, registryWith(provider))
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/admin/payout/vendors/plainvendor/force-fail", tokenForUser(t, uuid.New(), "admin"), `{"fail":true}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAdminRouter_ForceFail_Success_TripsSubsequentSubmit(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	provider := mockvendor.NewPayoutProvider("mockvendor")
	m := newTestModule(repo, stubPoster{}, registryWith(provider))
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodPost, "/admin/payout/vendors/mockvendor/force-fail", tokenForUser(t, uuid.New(), "admin"), `{"fail":true}`)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"force_fail":true`)

	dest, _ := json.Marshal(map[string]string{"bank_code": "014", "account_no": "1"})
	_, err := provider.Submit(context.Background(), "any-key", decimal.NewFromInt(1000), "IDR", dest)
	assert.Error(t, err, "force-fail must trip EVERY Submit against this vendor regardless of destination content")

	w2 := doReq(t, router, http.MethodPost, "/admin/payout/vendors/mockvendor/force-fail", tokenForUser(t, uuid.New(), "admin"), `{"fail":false}`)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Contains(t, w2.Body.String(), `"force_fail":false`)

	_, err = provider.Submit(context.Background(), "any-key-2", decimal.NewFromInt(1000), "IDR", dest)
	assert.NoError(t, err, "flipping the switch back off must restore normal behavior")
}

// ─── Admin: vendor health (docs/roadmap/archive/40 Task T5) ────────────────────────

func TestAdminRouter_VendorHealth_NonAdmin_403(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/admin/payout/vendors/health", tokenForUser(t, uuid.New(), "user"), "")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAdminRouter_VendorHealth_NilBreaker_EmptyList(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)                     // no calls expected
	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry()) // breaker deliberately nil
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/admin/payout/vendors/health", tokenForUser(t, uuid.New(), "admin"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"vendors":[]`)
}

// TestAdminRouter_VendorHealth_ReportsAllThreeStates is docs/roadmap/archive/40 Task
// T5's own required test: a tracker seeded with closed, open, AND
// half-open vendors must report each state accurately in one snapshot.
func TestAdminRouter_VendorHealth_ReportsAllThreeStates(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	breaker := vendorgw.NewHealthTracker(1, time.Nanosecond, nil)
	breaker.RecordFailure(context.Background(), "open-vendor") // trips open, cooldown effectively never elapses in this test
	breaker.RecordFailure(context.Background(), "half-open-vendor")
	assert.True(t, breaker.Allow(context.Background(), "half-open-vendor"), "cooldown of 1ns has elapsed by the time Allow is called, promoting to half-open")
	// "closed-vendor" is never touched — stays closed by default.

	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	m.breaker = breaker
	router := newAdminTestRouter(m)

	w := doReq(t, router, http.MethodGet, "/admin/payout/vendors/health", tokenForUser(t, uuid.New(), "admin"), "")
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
	assert.Empty(t, states["closed-vendor"], "a never-touched vendor has no entry at all — closed is the implicit default, not a snapshot row")
}
