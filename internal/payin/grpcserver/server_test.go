package grpcserver

import (
	"context"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	"github.com/herdifirdausss/seev/internal/payin/model"
)

type fakeService struct {
	callback func(context.Context, string, string, string, string, string, string, string, string, string, string) (string, error)
}

func (f *fakeService) CreateTopupIntent(context.Context, uuid.UUID, decimal.Decimal) (model.TopupIntent, error) {
	return model.TopupIntent{}, nil
}

func (f *fakeService) GetTopupIntent(context.Context, uuid.UUID) (model.TopupIntent, error) {
	return model.TopupIntent{}, nil
}

func (f *fakeService) HandleVendorCallback(ctx context.Context, vendor, eventID, reference, amount, currency, callbackStatus, occurredAt, inboxID, requestID, unknownStatus string) (string, error) {
	return f.callback(ctx, vendor, eventID, reference, amount, currency, callbackStatus, occurredAt, inboxID, requestID, unknownStatus)
}

func TestHandleVendorCallback_DeliversNormalizedContract(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	service := &fakeService{callback: func(_ context.Context, vendor, eventID, reference, amount, currency, callbackStatus, occurredAt, inboxID, requestID, unknownStatus string) (string, error) {
		require.Equal(t, "mockvendor", vendor)
		require.Equal(t, "evt-1", eventID)
		require.Equal(t, "TOP-1", reference)
		require.Equal(t, "50000", amount)
		require.Equal(t, "IDR", currency)
		require.Equal(t, "settled", callbackStatus)
		require.Equal(t, "inbox-1", inboxID)
		require.Equal(t, "req-1", requestID)
		require.Empty(t, unknownStatus)
		return "finalized", nil
	}}
	server := grpc.NewServer()
	payinv1.RegisterPayinServiceServer(server, New(service, nil, nil, nil, nil, nil))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })

	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	client := payinv1.NewPayinServiceClient(connection)
	response, err := client.HandleVendorCallback(context.Background(), &payinv1.HandleVendorCallbackRequest{Vendor: "mockvendor", VendorEventId: "evt-1", ExternalReference: "TOP-1", Amount: "50000", Currency: "IDR", Status: "settled", VendorInboxId: "inbox-1", RequestId: "req-1"})
	require.NoError(t, err)
	require.Equal(t, payinv1.VendorCallbackResult_VENDOR_CALLBACK_RESULT_FINALIZED, response.GetResult())
}
