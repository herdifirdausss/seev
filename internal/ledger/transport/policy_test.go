package transport

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/pkg/middleware"
)

// fakePolicyChecker is a hand-written test double — PolicyChecker is a
// two-method interface, doesn't earn a generated mock.
type fakePolicyChecker struct {
	allowed      bool
	rule, detail string
	checkErr     error

	checkCalled  bool
	recordCalled bool
	recordAmount decimal.Decimal
}

func (f *fakePolicyChecker) Check(_ context.Context, _ uuid.UUID, _ string, _ decimal.Decimal) (bool, string, string, error) {
	f.checkCalled = true
	return f.allowed, f.rule, f.detail, f.checkErr
}

func (f *fakePolicyChecker) Record(_ context.Context, _ uuid.UUID, _ string, amount decimal.Decimal) {
	f.recordCalled = true
	f.recordAmount = amount
}

func newTestHandlerWithPolicy(t *testing.T, policy PolicyChecker) (http.Handler, *MockService, *gomock.Controller) {
	t.Helper()
	ctrl := gomock.NewController(t)
	svc := NewMockService(ctrl)
	// See newTestHandler's comment in http_test.go — NewRouterWithPolicy
	// always builds the public router, which resolves currency for fee
	// pricing (docs/plan/18 Task T2) before every post.
	svc.EXPECT().GetUserCurrency(gomock.Any(), gomock.Any(), gomock.Any()).Return("IDR", nil).AnyTimes()
	h := middleware.WithAuth(testSecret, "")(NewRouterWithPolicy(svc, policy))
	return h, svc, ctrl
}

func TestPostTransaction_NoPolicyConfigured_SkipsCheckEntirely(t *testing.T) {
	// NewRouter (no policy arg) must behave byte-identical to before this
	// feature existed — this is NewRouter's own contract test, not just
	// NewRouterWithPolicy(svc, nil)'s.
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).Return(nil)

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + uuid.NewString() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestPostTransaction_PolicyAllows_PostsAndRecords(t *testing.T) {
	pc := &fakePolicyChecker{allowed: true}
	h, svc, ctrl := newTestHandlerWithPolicy(t, pc)
	defer ctrl.Finish()
	userID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).Return(nil)

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + uuid.NewString() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)

	require.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, pc.checkCalled)
	assert.True(t, pc.recordCalled, "a successful post must record usage")
	assert.True(t, pc.recordAmount.Equal(decimal.NewFromInt(1000)))
}

func TestPostTransaction_PolicyRejects_Returns422AndNeverPosts(t *testing.T) {
	pc := &fakePolicyChecker{allowed: false, rule: "max_daily_amount", detail: "would exceed 10000"}
	h, _, ctrl := newTestHandlerWithPolicy(t, pc)
	defer ctrl.Finish()
	userID := uuid.New()

	// No svc.EXPECT().Post(...) — must never reach the service layer.
	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + uuid.NewString() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "max_daily_amount")
}

func TestPostTransaction_PostFails_PolicyNeverRecords(t *testing.T) {
	pc := &fakePolicyChecker{allowed: true}
	h, svc, ctrl := newTestHandlerWithPolicy(t, pc)
	defer ctrl.Finish()
	userID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).Return(assertAnError{})

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + uuid.NewString() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)

	require.NotEqual(t, http.StatusCreated, w.Code)
	assert.True(t, pc.checkCalled)
	assert.False(t, pc.recordCalled, "a failed post must NEVER consume policy quota")
}

func TestPostTransaction_PolicyCheckErrors_Returns500(t *testing.T) {
	pc := &fakePolicyChecker{checkErr: assertAnError{}}
	h, _, ctrl := newTestHandlerWithPolicy(t, pc)
	defer ctrl.Finish()
	userID := uuid.New()

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + uuid.NewString() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPostTransaction_InternalRouter_NeverGetsPolicyChecked(t *testing.T) {
	// NewInternalRouter has no policy parameter at all — trusted internal
	// callers are never subject to end-user velocity limits.
	h, svc, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).Return(nil)

	body := `{"idempotency_key":"abc12345","type":"money_in","amount":"1000","metadata":{"gateway":"bca"}}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

type assertAnError struct{}

func (assertAnError) Error() string { return "boom" }
