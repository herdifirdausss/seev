package grpcserver

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	payoutv1 "github.com/herdifirdausss/seev/gen/go/payout/v1"
	"github.com/herdifirdausss/seev/services/payout/internal/payout/model"
)

// merchantFakeService extends fakeService with the optional merchant RPCs
// (Plan 57 C1 B2B HTTP handlers follow-up) — a separate fake rather than
// growing fakeService, so the non-merchant tests in server_test.go keep
// proving the "unimplemented when the service doesn't support it" branch.
// Get is inherited unchanged from fakeService — CreateMerchantPayout's
// handler reads the created row back through the base Service.Get, not a
// merchant-specific read.
type merchantFakeService struct {
	fakeService
	createFn func(context.Context, uuid.UUID, string, string, decimal.Decimal, []byte, string, string) (uuid.UUID, error)
	getFn    func(context.Context, uuid.UUID, uuid.UUID) (model.PayoutRequest, error)
}

func (f *merchantFakeService) CreateMerchant(ctx context.Context, tenantID uuid.UUID, environment, currency string, amount decimal.Decimal, destination []byte, createdBy, downstreamKey string) (uuid.UUID, error) {
	return f.createFn(ctx, tenantID, environment, currency, amount, destination, createdBy, downstreamKey)
}

func (f *merchantFakeService) GetMerchant(ctx context.Context, tenantID, id uuid.UUID) (model.PayoutRequest, error) {
	return f.getFn(ctx, tenantID, id)
}

func merchantTestClient(t *testing.T, service Service, notFound, noVendorAvailable, sandboxVendorUnavailable error) payoutv1.PayoutServiceClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	payoutv1.RegisterPayoutServiceServer(server, New(service, notFound, errors.New("no route"), noVendorAvailable, errors.New("screening blocked"), errors.New("screening dependency unavailable"), sandboxVendorUnavailable))
	go func() { _ = server.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close(); server.Stop(); _ = listener.Close() })
	return payoutv1.NewPayoutServiceClient(connection)
}

func TestCreateMerchantPayout_Success(t *testing.T) {
	tenantID := uuid.New()
	requestID := uuid.New()
	service := &merchantFakeService{
		fakeService: fakeService{created: model.PayoutRequest{ID: requestID, MerchantTenantID: tenantID, Vendor: "mockvendor", Currency: "IDR", Status: model.StatusSubmitted}},
		createFn: func(_ context.Context, gotTenant uuid.UUID, environment, currency string, amount decimal.Decimal, destination []byte, createdBy, downstreamKey string) (uuid.UUID, error) {
			assert.Equal(t, tenantID, gotTenant)
			assert.Equal(t, "live", environment)
			assert.Equal(t, "IDR", currency)
			assert.True(t, amount.Equal(decimal.NewFromInt(100000)))
			assert.Equal(t, []byte(`{"account_no":"001"}`), destination)
			assert.Equal(t, "dkey-1", downstreamKey)
			return requestID, nil
		},
	}
	client := merchantTestClient(t, service, errors.New("not found"), errors.New("no vendor"), errors.New("sandbox unavailable"))

	resp, err := client.CreateMerchantPayout(context.Background(), &payoutv1.CreateMerchantPayoutRequest{
		TenantId: tenantID.String(), Environment: "live", Currency: "IDR", Amount: "100000",
		Destination: []byte(`{"account_no":"001"}`), CreatedBy: "gateway", DownstreamKey: "dkey-1",
	})
	require.NoError(t, err)
	assert.Equal(t, requestID.String(), resp.GetPayout().GetId())
}

func TestCreateMerchantPayout_MissingDestination_InvalidArgument(t *testing.T) {
	service := &merchantFakeService{}
	client := merchantTestClient(t, service, errors.New("not found"), errors.New("no vendor"), errors.New("sandbox unavailable"))

	_, err := client.CreateMerchantPayout(context.Background(), &payoutv1.CreateMerchantPayoutRequest{
		TenantId: uuid.NewString(), Environment: "live", Currency: "IDR", Amount: "100000", DownstreamKey: "dkey-1",
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateMerchantPayout_SandboxVendorUnavailable_MapsToFailedPrecondition(t *testing.T) {
	sandboxErr := errors.New("sandbox unavailable")
	service := &merchantFakeService{createFn: func(context.Context, uuid.UUID, string, string, decimal.Decimal, []byte, string, string) (uuid.UUID, error) {
		return uuid.Nil, sandboxErr
	}}
	client := merchantTestClient(t, service, errors.New("not found"), errors.New("no vendor"), sandboxErr)

	_, err := client.CreateMerchantPayout(context.Background(), &payoutv1.CreateMerchantPayoutRequest{
		TenantId: uuid.NewString(), Environment: "sandbox", Currency: "IDR", Amount: "100000",
		Destination: []byte(`{}`), DownstreamKey: "dkey-1",
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestGetMerchantPayout_NotFound(t *testing.T) {
	notFound := errors.New("not found")
	service := &merchantFakeService{getFn: func(context.Context, uuid.UUID, uuid.UUID) (model.PayoutRequest, error) {
		return model.PayoutRequest{}, notFound
	}}
	client := merchantTestClient(t, service, notFound, errors.New("no vendor"), errors.New("sandbox unavailable"))

	_, err := client.GetMerchantPayout(context.Background(), &payoutv1.GetMerchantPayoutRequest{TenantId: uuid.NewString(), Id: uuid.NewString()})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetMerchantPayout_Success(t *testing.T) {
	tenantID := uuid.New()
	id := uuid.New()
	service := &merchantFakeService{getFn: func(_ context.Context, gotTenant, gotID uuid.UUID) (model.PayoutRequest, error) {
		assert.Equal(t, tenantID, gotTenant)
		assert.Equal(t, id, gotID)
		return model.PayoutRequest{ID: id, MerchantTenantID: tenantID, Vendor: "mockvendor", Status: model.StatusSettled}, nil
	}}
	client := merchantTestClient(t, service, errors.New("not found"), errors.New("no vendor"), errors.New("sandbox unavailable"))

	resp, err := client.GetMerchantPayout(context.Background(), &payoutv1.GetMerchantPayoutRequest{TenantId: tenantID.String(), Id: id.String()})
	require.NoError(t, err)
	assert.Equal(t, id.String(), resp.GetPayout().GetId())
}

// TestCreateMerchantPayout_ServiceWithoutMerchantSupport_Unimplemented
// proves the type-assertion fallback: a Service that only implements the
// core (non-merchant) contract must fail closed with Unimplemented, never
// panic or silently no-op.
func TestCreateMerchantPayout_ServiceWithoutMerchantSupport_Unimplemented(t *testing.T) {
	service := &fakeService{}
	client := merchantTestClient(t, service, errors.New("not found"), errors.New("no vendor"), errors.New("sandbox unavailable"))

	_, err := client.CreateMerchantPayout(context.Background(), &payoutv1.CreateMerchantPayoutRequest{
		TenantId: uuid.NewString(), Environment: "live", Currency: "IDR", Amount: "100000",
		Destination: []byte(`{}`), DownstreamKey: "dkey-1",
	})
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}
