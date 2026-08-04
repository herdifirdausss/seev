package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vendorgw "github.com/herdifirdausss/seev/contracts/vendorgw"
	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
	"github.com/herdifirdausss/seev/services/payin/internal/payin/model"
	service "github.com/herdifirdausss/seev/services/payin/internal/payin"
)

const testSecret = "supersecretkeythatisatleast32chars!"

type fakeService struct {
	events       []service.WebhookEvent
	replayErr    error
	vendorHealth []vendorgw.VendorHealth
}

func (f *fakeService) ApplyIntakeControl(context.Context, uuid.UUID, string, int64, string, string) (service.IntakeCommandResult, error) {
	return service.IntakeCommandResult{Applied: true}, nil
}
func (f *fakeService) VendorHealth(context.Context) []vendorgw.VendorHealth { return f.vendorHealth }
func (f *fakeService) ListEvents(context.Context, string, string, int, int) ([]service.WebhookEvent, error) {
	return f.events, nil
}
func (f *fakeService) ReplayEvent(context.Context, uuid.UUID) error { return f.replayErr }
func (f *fakeService) NewRoutingRule(input service.RoutingRuleInput) (model.RoutingRule, error) {
	return model.RoutingRule{ID: uuid.New(), Flow: input.Flow, Vendor: input.Vendor, Enabled: true}, nil
}
func (f *fakeService) ListRoutingRules(context.Context) ([]model.RoutingRule, error) { return nil, nil }
func (f *fakeService) CreateRoutingRule(context.Context, model.RoutingRule) error    { return nil }
func (f *fakeService) UpdateRoutingRule(context.Context, model.RoutingRule) error    { return nil }
func (f *fakeService) GetVendorGateway(context.Context, string) (model.VendorGateway, bool, error) {
	return model.VendorGateway{}, false, nil
}
func (f *fakeService) ValidateVendorGateway(vendor, gateway string) (model.VendorGateway, error) {
	if vendor == "" || gateway == "" {
		return model.VendorGateway{}, errors.New("invalid gateway")
	}
	return model.VendorGateway{Vendor: vendor, Gateway: gateway}, nil
}
func (f *fakeService) UpsertVendorGateway(context.Context, model.VendorGateway) error { return nil }
func (f *fakeService) PrivacyExportPage(context.Context, uuid.UUID, time.Time, int, int) ([]json.RawMessage, string, error) {
	return nil, "", nil
}
func (f *fakeService) PrivacyPrepareClosure(context.Context, uuid.UUID) (bool, []string, error) {
	return false, nil, nil
}
func (f *fakeService) PrivacyCommitClosure(context.Context, uuid.UUID, uuid.UUID) (string, int, error) {
	return "", 0, nil
}

func newAdminTestRouter(t *testing.T, serviceModule Service) http.Handler {
	t.Helper()
	return middleware.WithAuth(testSecret, "")(New(serviceModule).AdminRouter())
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

func TestAdminRouter_Authorization(t *testing.T) {
	fake := &fakeService{}
	router := newAdminTestRouter(t, fake)

	assert.Equal(t, http.StatusForbidden, doAdminReq(t, router, http.MethodGet, "/admin/payin/events", tokenFor(t, "user")).Code)
	assert.Equal(t, http.StatusUnauthorized, doAdminReq(t, router, http.MethodGet, "/admin/payin/events", "").Code)
}

func TestAdminRouter_ListEvents(t *testing.T) {
	fake := &fakeService{events: []service.WebhookEvent{{Vendor: "mockvendor", Status: "posted", Amount: decimal.NewFromInt(1000)}}}
	w := doAdminReq(t, newAdminTestRouter(t, fake), http.MethodGet, "/admin/payin/events", tokenFor(t, "admin"))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "mockvendor")
}

func TestAdminRouter_VendorHealth(t *testing.T) {
	breaker := vendorgw.NewHealthTracker(1, time.Nanosecond, nil)
	breaker.RecordFailure(context.Background(), "open-vendor")
	fake := &fakeService{vendorHealth: breaker.Snapshot(context.Background())}
	w := doAdminReq(t, newAdminTestRouter(t, fake), http.MethodGet, "/admin/payin/vendors/health", tokenFor(t, "admin"))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "open-vendor")
}

func TestAdminRouter_ReplayEventErrors(t *testing.T) {
	id := uuid.New()
	fake := &fakeService{replayErr: service.ErrAlreadyPosted}
	w := doAdminReq(t, newAdminTestRouter(t, fake), http.MethodPost, "/admin/payin/events/"+id.String()+"/replay", tokenFor(t, "admin"))
	assert.Equal(t, http.StatusConflict, w.Code)

	fake.replayErr = service.ErrEventNotFound
	w = doAdminReq(t, newAdminTestRouter(t, fake), http.MethodPost, "/admin/payin/events/"+id.String()+"/replay", tokenFor(t, "admin"))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
