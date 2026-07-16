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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

func withUser(t *testing.T, handler http.Handler) http.Handler {
	t.Helper()
	return middleware.WithAuth(testConfig().JWT.Secret, "")(handler)
}

func userToken(t *testing.T, id uuid.UUID) string {
	t.Helper()
	token, err := middleware.GenerateToken(testConfig().JWT.Secret, middleware.Claims{UserID: id.String(), Role: "user", Exp: time.Now().Add(time.Hour).Unix()})
	assert.NoError(t, err)
	return token
}

func TestCreateTopupGateway_ForwardsWithoutVendorAndPreservesEnvelope(t *testing.T) {
	userID := uuid.New()
	now := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	client := fakePayinClient{create: func(_ context.Context, request *payinv1.CreateTopupIntentRequest) (*payinv1.CreateTopupIntentResponse, error) {
		assert.Equal(t, userID.String(), request.GetUserId())
		assert.Equal(t, "500000", request.GetAmount())
		return &payinv1.CreateTopupIntentResponse{Intent: &payinv1.TopupIntent{Id: uuid.NewString(), Reference: "TOP-ref", UserId: userID.String(), Amount: "500000", Currency: "IDR", Vendor: "mockvendor", Status: "pending", ExpiresAt: timestamppb.New(now), CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)}}, nil
	}}
	h := withUser(t, createTopupIntentHandler(client))
	req := httptest.NewRequest(http.MethodPost, "/topup", strings.NewReader(`{"amount":"500000"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userToken(t, userID))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), `"vendor":"mockvendor"`)
	assert.Contains(t, w.Body.String(), `"success":true`)
}

func TestCreateTopupGateway_NoRoute422(t *testing.T) {
	client := fakePayinClient{create: func(context.Context, *payinv1.CreateTopupIntentRequest) (*payinv1.CreateTopupIntentResponse, error) {
		return nil, status.Error(codes.FailedPrecondition, "no route")
	}}
	id := uuid.New()
	h := withUser(t, createTopupIntentHandler(client))
	req := httptest.NewRequest(http.MethodPost, "/topup", strings.NewReader(`{"amount":"100"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userToken(t, id))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"NO_ROUTE"`)
}
