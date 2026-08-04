package client

import (
	"context"
	"errors"
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

	payinv1 "github.com/herdifirdausss/seev/gen/go/payin/v1"
)

// fakePayinServer implements just the two merchant RPCs this package
// calls; every other method falls back to
// UnimplementedPayinServiceServer's own codes.Unimplemented.
type fakePayinServer struct {
	payinv1.UnimplementedPayinServiceServer
	createResp *payinv1.CreateMerchantTopupIntentResponse
	createErr  error
	getResp    *payinv1.GetMerchantTopupIntentResponse
	getErr     error
	gotCreate  *payinv1.CreateMerchantTopupIntentRequest
	gotGet     *payinv1.GetMerchantTopupIntentRequest
}

func (f *fakePayinServer) CreateMerchantTopupIntent(_ context.Context, req *payinv1.CreateMerchantTopupIntentRequest) (*payinv1.CreateMerchantTopupIntentResponse, error) {
	f.gotCreate = req
	return f.createResp, f.createErr
}

func (f *fakePayinServer) GetMerchantTopupIntent(_ context.Context, req *payinv1.GetMerchantTopupIntentRequest) (*payinv1.GetMerchantTopupIntentResponse, error) {
	f.gotGet = req
	return f.getResp, f.getErr
}

func newPayinClientForTest(t *testing.T, srv payinv1.PayinServiceServer) *PayinClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	payinv1.RegisterPayinServiceServer(server, srv)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(); server.Stop(); _ = listener.Close() })
	return NewPayinClient(payinv1.NewPayinServiceClient(conn))
}

func TestPayinClient_CreateTopupIntent_Success(t *testing.T) {
	tenantID := uuid.New()
	intentID := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	fake := &fakePayinServer{createResp: &payinv1.CreateMerchantTopupIntentResponse{Intent: &payinv1.TopupIntent{
		Id: intentID.String(), Status: "pending", Currency: "IDR", Amount: "50000", Vendor: "mockvendor",
		CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now),
	}}}
	client := newPayinClientForTest(t, fake)

	result, err := client.CreateTopupIntent(context.Background(), tenantID, "live", "IDR", "50000", "dkey-1")
	require.NoError(t, err)
	assert.Equal(t, intentID, result.ID)
	assert.Equal(t, "pending", result.Status)
	assert.Equal(t, "mockvendor", result.Vendor)
	assert.Equal(t, tenantID.String(), fake.gotCreate.GetTenantId())
	assert.Equal(t, "dkey-1", fake.gotCreate.GetDownstreamKey())
}

func TestPayinClient_GetTopupIntent_NotFound_TranslatesToErrNotFound(t *testing.T) {
	fake := &fakePayinServer{getErr: status.Error(codes.NotFound, "topup intent not found")}
	client := newPayinClientForTest(t, fake)

	_, err := client.GetTopupIntent(context.Background(), uuid.New(), uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestPayinClient_CreateTopupIntent_Unavailable_TranslatesToErrOwnerUnavailable(t *testing.T) {
	fake := &fakePayinServer{createErr: status.Error(codes.Unavailable, "no vendor available")}
	client := newPayinClientForTest(t, fake)

	_, err := client.CreateTopupIntent(context.Background(), uuid.New(), "live", "IDR", "50000", "dkey-1")
	assert.ErrorIs(t, err, ErrOwnerUnavailable)
}

func TestPayinClient_CreateTopupIntent_InvalidArgument_TranslatesToErrValidation(t *testing.T) {
	fake := &fakePayinServer{createErr: status.Error(codes.InvalidArgument, "amount must be a positive integer decimal string")}
	client := newPayinClientForTest(t, fake)

	_, err := client.CreateTopupIntent(context.Background(), uuid.New(), "live", "IDR", "not-a-number", "dkey-1")
	assert.ErrorIs(t, err, ErrValidation)
}

func TestTranslateError_NonStatusError_TranslatesToErrOwnerUnavailable(t *testing.T) {
	err := translateError(errors.New("connection refused"))
	assert.ErrorIs(t, err, ErrOwnerUnavailable)
}
