package http

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

	vendorgw "github.com/herdifirdausss/seev/contracts/vendorgw"
	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
	"github.com/herdifirdausss/seev/services/payout/internal/payout/model"
	service "github.com/herdifirdausss/seev/services/payout/internal/payout"
)

const testSecret = "supersecretkeythatisatleast32chars!"

type fakeService struct {
	request      service.PayoutRequest
	getErr       error
	vendorHealth []vendorgw.VendorHealth
}

func (f *fakeService) Create(context.Context, uuid.UUID, decimal.Decimal, []byte, string, string) (uuid.UUID, error) {
	return f.request.ID, nil
}
func (f *fakeService) Get(context.Context, uuid.UUID) (service.PayoutRequest, error) {
	return f.request, f.getErr
}
func (f *fakeService) List(context.Context, string, string, int, int) ([]service.PayoutRequest, error) {
	return []service.PayoutRequest{f.request}, nil
}
func (f *fakeService) AdminCancel(context.Context, uuid.UUID, string) error { return nil }
func (f *fakeService) AdminRetry(context.Context, uuid.UUID) error          { return nil }
func (f *fakeService) ApplyIntakeControl(context.Context, uuid.UUID, string, int64, string, string) (service.IntakeCommandResult, error) {
	return service.IntakeCommandResult{Applied: true}, nil
}
func (f *fakeService) ListDeadVendorCommands(context.Context, int, int) ([]model.PayoutVendorCommand, error) {
	return nil, nil
}
func (f *fakeService) ReplayDeadVendorCommand(context.Context, uuid.UUID) error { return nil }
func (f *fakeService) ReplayAllDeadVendorCommands(context.Context, time.Time) (int, error) {
	return 0, nil
}
func (f *fakeService) VendorHealth(context.Context) []vendorgw.VendorHealth { return f.vendorHealth }
func (f *fakeService) ForceFailVendor(string, bool) error                   { return nil }
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

func tokenFor(t *testing.T, role string) string {
	t.Helper()
	tok, err := middleware.GenerateToken(testSecret, middleware.Claims{UserID: uuid.New().String(), Role: role, Exp: time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	return tok
}

func doRequest(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestAdminRouter_Authorization(t *testing.T) {
	fake := &fakeService{}
	router := middleware.WithAuth(testSecret, "")(New(fake).AdminRouter())
	assert.Equal(t, http.StatusForbidden, doRequest(t, router, http.MethodGet, "/admin/payout/requests", tokenFor(t, "user"), "").Code)
	assert.Equal(t, http.StatusUnauthorized, doRequest(t, router, http.MethodGet, "/admin/payout/requests", "", "").Code)
}

func TestAdminRouter_ListRequests(t *testing.T) {
	requestID := uuid.New()
	fake := &fakeService{request: service.PayoutRequest{ID: requestID, UserID: uuid.New(), Amount: decimal.NewFromInt(100), Currency: "IDR", Vendor: "mock", Status: "pending"}}
	router := middleware.WithAuth(testSecret, "")(New(fake).AdminRouter())
	w := doRequest(t, router, http.MethodGet, "/admin/payout/requests", tokenFor(t, "admin"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), requestID.String())
}

func TestPublicHandlers_RequireIdentityAndValidateInput(t *testing.T) {
	fake := &fakeService{request: service.PayoutRequest{ID: uuid.New()}}
	mux := http.NewServeMux()
	mux.Handle("POST /payout", New(fake).CreateHandler())
	mux.Handle("GET /payout/{id}", New(fake).GetHandler())
	assert.Equal(t, http.StatusUnauthorized, doRequest(t, mux, http.MethodPost, "/payout", "", `{}`).Code)
	assert.Equal(t, http.StatusBadRequest, doRequest(t, middleware.WithAuth(testSecret, "")(mux), http.MethodPost, "/payout", tokenFor(t, "user"), `{"amount":"1.5","destination":{}}`).Code)
}

func TestAdminRouter_VendorHealth(t *testing.T) {
	breaker := vendorgw.NewHealthTracker(1, time.Nanosecond, nil)
	breaker.RecordFailure(context.Background(), "open-vendor")
	fake := &fakeService{vendorHealth: breaker.Snapshot(context.Background())}
	router := middleware.WithAuth(testSecret, "")(New(fake).AdminRouter())
	w := doRequest(t, router, http.MethodGet, "/admin/payout/vendors/health", tokenFor(t, "admin"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "open-vendor")
}
