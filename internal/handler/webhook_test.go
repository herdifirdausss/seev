package handler

// fakePayinClient is shared by the Gateway's top-up tests. Vendor callbacks
// are no longer tested here because Gateway deliberately has no webhook route;
// VendorService owns that boundary.

import (
	"context"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakePayinClient struct {
	create func(context.Context, *payinv1.CreateTopupIntentRequest) (*payinv1.CreateTopupIntentResponse, error)
	get    func(context.Context, *payinv1.GetTopupIntentRequest) (*payinv1.GetTopupIntentResponse, error)
}

func (f fakePayinClient) HandleWebhook(context.Context, *payinv1.HandleWebhookRequest, ...grpc.CallOption) (*payinv1.HandleWebhookResponse, error) {
	return nil, status.Error(codes.Unimplemented, "legacy Gateway webhook route removed")
}

func (f fakePayinClient) HandleVendorCallback(context.Context, *payinv1.HandleVendorCallbackRequest, ...grpc.CallOption) (*payinv1.HandleVendorCallbackResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not used by Gateway")
}

func (f fakePayinClient) CreateTopupIntent(ctx context.Context, request *payinv1.CreateTopupIntentRequest, _ ...grpc.CallOption) (*payinv1.CreateTopupIntentResponse, error) {
	return f.create(ctx, request)
}

func (f fakePayinClient) GetTopupIntent(ctx context.Context, request *payinv1.GetTopupIntentRequest, _ ...grpc.CallOption) (*payinv1.GetTopupIntentResponse, error) {
	if f.get == nil {
		return nil, status.Error(codes.Unimplemented, "get topup not configured")
	}
	return f.get(ctx, request)
}

func (f fakePayinClient) ListAssuranceRecords(context.Context, *payinv1.ListAssuranceRecordsRequest, ...grpc.CallOption) (*payinv1.ListAssuranceRecordsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not used by Gateway")
}

func (f fakePayinClient) GetIntakeControl(context.Context, *payinv1.GetIntakeControlRequest, ...grpc.CallOption) (*payinv1.GetIntakeControlResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not used by Gateway")
}

func (f fakePayinClient) ApplyIntakeControl(context.Context, *payinv1.ApplyIntakeControlRequest, ...grpc.CallOption) (*payinv1.ApplyIntakeControlResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not used by Gateway")
}
