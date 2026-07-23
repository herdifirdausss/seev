package handler

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
)

type fakePayinClient struct {
	handle func(context.Context, *payinv1.HandleWebhookRequest) (*payinv1.HandleWebhookResponse, error)
	create func(context.Context, *payinv1.CreateTopupIntentRequest) (*payinv1.CreateTopupIntentResponse, error)
	get    func(context.Context, *payinv1.GetTopupIntentRequest) (*payinv1.GetTopupIntentResponse, error)
}

func (f fakePayinClient) ListAssuranceRecords(context.Context, *payinv1.ListAssuranceRecordsRequest, ...grpc.CallOption) (*payinv1.ListAssuranceRecordsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
func (f fakePayinClient) GetIntakeControl(context.Context, *payinv1.GetIntakeControlRequest, ...grpc.CallOption) (*payinv1.GetIntakeControlResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
func (f fakePayinClient) ApplyIntakeControl(context.Context, *payinv1.ApplyIntakeControlRequest, ...grpc.CallOption) (*payinv1.ApplyIntakeControlResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (f fakePayinClient) HandleWebhook(ctx context.Context, request *payinv1.HandleWebhookRequest, _ ...grpc.CallOption) (*payinv1.HandleWebhookResponse, error) {
	return f.handle(ctx, request)
}
func (f fakePayinClient) CreateTopupIntent(ctx context.Context, request *payinv1.CreateTopupIntentRequest, _ ...grpc.CallOption) (*payinv1.CreateTopupIntentResponse, error) {
	if f.create == nil {
		return nil, status.Error(codes.Unimplemented, "")
	}
	return f.create(ctx, request)
}
func (f fakePayinClient) GetTopupIntent(ctx context.Context, request *payinv1.GetTopupIntentRequest, _ ...grpc.CallOption) (*payinv1.GetTopupIntentResponse, error) {
	if f.get == nil {
		return nil, status.Error(codes.Unimplemented, "")
	}
	return f.get(ctx, request)
}
func payinReturning(response *payinv1.HandleWebhookResponse, err error) fakePayinClient {
	return fakePayinClient{handle: func(context.Context, *payinv1.HandleWebhookRequest) (*payinv1.HandleWebhookResponse, error) {
		return response, err
	}}
}

// TestWebhookHandler_BodyOverCap_413 calls webhookHandler directly —
// bypassing the full middleware chain (specifically WithLogger, whose
// own 16KiB request-body log-snippet cap currently truncates bodies
// before they reach any handler, a separate pre-existing bug tracked
// elsewhere) — to prove this handler's OWN 64KiB cap (docs/roadmap/archive/22 Task
// T3) is correctly enforced, independent of that issue.
func TestWebhookHandler_BodyOverCap_413(t *testing.T) {
	deps := &Dependencies{Payin: payinReturning(nil, status.Error(codes.Internal, ""))}

	oversized := strings.Repeat("a", 70<<10) // > maxWebhookBodyBytes (64KiB)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/mockvendor", strings.NewReader(oversized))
	req.SetPathValue("vendor", "mockvendor")
	w := httptest.NewRecorder()

	webhookHandler(deps, slog.Default())(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

// TestWebhookHandler_BodyAtCap_NotRejectedForSize proves the cap is
// exclusive (> maxWebhookBodyBytes, not >=) — a body exactly at the limit
// must not be rejected for its size (it may still fail for other reasons,
// e.g. unknown vendor, which is fine — this test only asserts it's not 413).
func TestWebhookHandler_BodyAtCap_NotRejectedForSize(t *testing.T) {
	deps := &Dependencies{Payin: payinReturning(nil, status.Error(codes.Internal, ""))}

	atCap := strings.Repeat("a", maxWebhookBodyBytes)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/mockvendor", strings.NewReader(atCap))
	req.SetPathValue("vendor", "mockvendor")
	w := httptest.NewRecorder()

	webhookHandler(deps, slog.Default())(w, req)

	assert.NotEqual(t, http.StatusRequestEntityTooLarge, w.Code)
}

// TestWebhookHandler_NoPayinConfigured_404 proves the byte-identical-when-
// off default (docs/roadmap/archive/22 Task T3 DoD) at the handler level directly.
func TestWebhookHandler_NoPayinConfigured_404(t *testing.T) {
	deps := &Dependencies{} // Payin left nil
	req := httptest.NewRequest(http.MethodPost, "/webhooks/mockvendor", strings.NewReader(`{}`))
	req.SetPathValue("vendor", "mockvendor")
	w := httptest.NewRecorder()

	webhookHandler(deps, slog.Default())(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestWebhookHandler_GRPCMappingAndRawBody(t *testing.T) {
	raw := []byte{'{', 0, 0xff, '}'}
	tests := []struct {
		name     string
		response *payinv1.HandleWebhookResponse
		err      error
		want     int
	}{
		{"ok", &payinv1.HandleWebhookResponse{Result: payinv1.WebhookResult_WEBHOOK_RESULT_OK}, nil, http.StatusOK},
		{"business", &payinv1.HandleWebhookResponse{Result: payinv1.WebhookResult_WEBHOOK_RESULT_BUSINESS_FAILURE}, nil, http.StatusOK},
		{"unknown", nil, status.Error(codes.NotFound, ""), http.StatusNotFound},
		{"signature", nil, status.Error(codes.Unauthenticated, ""), http.StatusUnauthorized},
		{"infra", nil, status.Error(codes.Internal, ""), http.StatusServiceUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []byte
			client := fakePayinClient{handle: func(_ context.Context, request *payinv1.HandleWebhookRequest) (*payinv1.HandleWebhookResponse, error) {
				got = append([]byte(nil), request.GetRawBody()...)
				return tc.response, tc.err
			}}
			deps := &Dependencies{Payin: client}
			req := httptest.NewRequest(http.MethodPost, "/webhooks/mockvendor", strings.NewReader(string(raw)))
			req.SetPathValue("vendor", "mockvendor")
			w := httptest.NewRecorder()
			webhookHandler(deps, slog.Default())(w, req)
			assert.Equal(t, tc.want, w.Code)
			assert.Equal(t, raw, got)
		})
	}
}
