package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
)

type fakePayoutClient struct {
	create func(context.Context, *payoutv1.CreatePayoutRequest) (*payoutv1.CreatePayoutResponse, error)
	get    func(context.Context, *payoutv1.GetPayoutRequest) (*payoutv1.GetPayoutResponse, error)
}

func (f fakePayoutClient) ListAssuranceRecords(context.Context, *payoutv1.ListAssuranceRecordsRequest, ...grpc.CallOption) (*payoutv1.ListAssuranceRecordsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
func (f fakePayoutClient) GetIntakeControl(context.Context, *payoutv1.GetIntakeControlRequest, ...grpc.CallOption) (*payoutv1.GetIntakeControlResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
func (f fakePayoutClient) ApplyIntakeControl(context.Context, *payoutv1.ApplyIntakeControlRequest, ...grpc.CallOption) (*payoutv1.ApplyIntakeControlResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (f fakePayoutClient) CreatePayout(ctx context.Context, r *payoutv1.CreatePayoutRequest, _ ...grpc.CallOption) (*payoutv1.CreatePayoutResponse, error) {
	return f.create(ctx, r)
}
func (f fakePayoutClient) GetPayout(ctx context.Context, r *payoutv1.GetPayoutRequest, _ ...grpc.CallOption) (*payoutv1.GetPayoutResponse, error) {
	if f.get == nil {
		return nil, status.Error(codes.Unimplemented, "")
	}
	return f.get(ctx, r)
}

func TestPayoutGatewayCreatePreservesEnvelopeAndDestination(t *testing.T) {
	user := uuid.New()
	now := time.Now()
	client := fakePayoutClient{create: func(_ context.Context, r *payoutv1.CreatePayoutRequest) (*payoutv1.CreatePayoutResponse, error) {
		assert.Equal(t, user.String(), r.GetUserId())
		assert.JSONEq(t, `{"bank":"bca"}`, string(r.GetDestination()))
		return &payoutv1.CreatePayoutResponse{Payout: &payoutv1.Payout{Id: uuid.NewString(), UserId: user.String(), Amount: "50000", Currency: "IDR", Vendor: "mockvendor", Status: "settled", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)}}, nil
	}}
	h := withUser(t, createPayoutHandler(client))
	req := httptest.NewRequest(http.MethodPost, "/payout", strings.NewReader(`{"amount":"50000","destination":{"bank":"bca"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userToken(t, user))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), `"vendor":"mockvendor"`)
	assert.Contains(t, w.Body.String(), `"success":true`)
}
func TestPayoutGatewayRejectsClientVendor(t *testing.T) {
	user := uuid.New()
	client := fakePayoutClient{create: func(context.Context, *payoutv1.CreatePayoutRequest) (*payoutv1.CreatePayoutResponse, error) {
		panic("must not call")
	}}
	h := withUser(t, createPayoutHandler(client))
	req := httptest.NewRequest(http.MethodPost, "/payout", strings.NewReader(`{"amount":"1","vendor":"mockvendor","destination":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userToken(t, user))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
func TestPayoutGatewayNoRoute(t *testing.T) {
	user := uuid.New()
	client := fakePayoutClient{create: func(context.Context, *payoutv1.CreatePayoutRequest) (*payoutv1.CreatePayoutResponse, error) {
		return nil, status.Error(codes.FailedPrecondition, "no payout route available")
	}}
	h := withUser(t, createPayoutHandler(client))
	req := httptest.NewRequest(http.MethodPost, "/payout", strings.NewReader(`{"amount":"1","destination":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userToken(t, user))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"NO_ROUTE"`)
}

// TestPayoutGatewayScreeningBlocked proves docs/plan/37 Task T5: a
// FailedPrecondition gRPC error whose message carries payout's own
// ErrScreeningBlocked sentinel maps to 422 SCREENING_BLOCKED — the same
// HTTP contract shape as the ledger's own SCREENING_BLOCKED (Task T3).
func TestPayoutGatewayScreeningBlocked(t *testing.T) {
	user := uuid.New()
	client := fakePayoutClient{create: func(context.Context, *payoutv1.CreatePayoutRequest) (*payoutv1.CreatePayoutResponse, error) {
		return nil, status.Error(codes.FailedPrecondition, "payout: screening blocked: over threshold")
	}}
	h := withUser(t, createPayoutHandler(client))
	req := httptest.NewRequest(http.MethodPost, "/payout", strings.NewReader(`{"amount":"1","destination":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userToken(t, user))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"SCREENING_BLOCKED"`)
}
