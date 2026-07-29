package client

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
)

// fakePayoutServer implements just the two merchant RPCs this package
// calls; every other method falls back to
// UnimplementedPayoutServiceServer's own codes.Unimplemented.
type fakePayoutServer struct {
	payoutv1.UnimplementedPayoutServiceServer
	createResp *payoutv1.CreateMerchantPayoutResponse
	createErr  error
	getResp    *payoutv1.GetMerchantPayoutResponse
	getErr     error
	gotCreate  *payoutv1.CreateMerchantPayoutRequest
	gotGet     *payoutv1.GetMerchantPayoutRequest
}

func (f *fakePayoutServer) CreateMerchantPayout(_ context.Context, req *payoutv1.CreateMerchantPayoutRequest) (*payoutv1.CreateMerchantPayoutResponse, error) {
	f.gotCreate = req
	return f.createResp, f.createErr
}

func (f *fakePayoutServer) GetMerchantPayout(_ context.Context, req *payoutv1.GetMerchantPayoutRequest) (*payoutv1.GetMerchantPayoutResponse, error) {
	f.gotGet = req
	return f.getResp, f.getErr
}

func newPayoutClientForTest(t *testing.T, srv payoutv1.PayoutServiceServer) *PayoutClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	payoutv1.RegisterPayoutServiceServer(server, srv)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(); server.Stop(); _ = listener.Close() })
	return NewPayoutClient(payoutv1.NewPayoutServiceClient(conn))
}

func TestPayoutClient_CreatePayout_Success(t *testing.T) {
	tenantID := uuid.New()
	requestID := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	fake := &fakePayoutServer{createResp: &payoutv1.CreateMerchantPayoutResponse{Payout: &payoutv1.Payout{
		Id: requestID.String(), Status: "submitted", Currency: "IDR", Amount: "100000", Vendor: "mockvendor",
		CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now),
	}}}
	client := newPayoutClientForTest(t, fake)

	result, err := client.CreatePayout(context.Background(), tenantID, "live", "IDR", "100000", []byte(`{"account_no":"001"}`), "gateway", "dkey-1")
	require.NoError(t, err)
	assert.Equal(t, requestID, result.ID)
	assert.Equal(t, "submitted", result.Status)
	assert.Equal(t, tenantID.String(), fake.gotCreate.GetTenantId())
	assert.Equal(t, "dkey-1", fake.gotCreate.GetDownstreamKey())
	assert.Equal(t, []byte(`{"account_no":"001"}`), fake.gotCreate.GetDestination())
}

func TestPayoutClient_GetPayout_NotFound_TranslatesToErrNotFound(t *testing.T) {
	fake := &fakePayoutServer{getErr: status.Error(codes.NotFound, "payout request not found")}
	client := newPayoutClientForTest(t, fake)

	_, err := client.GetPayout(context.Background(), uuid.New(), uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestPayoutClient_CreatePayout_Unavailable_TranslatesToErrOwnerUnavailable(t *testing.T) {
	fake := &fakePayoutServer{createErr: status.Error(codes.Unavailable, "no vendor available")}
	client := newPayoutClientForTest(t, fake)

	_, err := client.CreatePayout(context.Background(), uuid.New(), "live", "IDR", "100000", []byte(`{}`), "gateway", "dkey-1")
	assert.ErrorIs(t, err, ErrOwnerUnavailable)
}
