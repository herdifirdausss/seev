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

	payinv1 "github.com/herdifirdausss/seev/gen/go/payin/v1"
	"github.com/herdifirdausss/seev/services/payin/internal/payin/model"
)

// merchantFakeService extends fakeService with the optional merchant RPCs
// (Plan 57 C1 B2B HTTP handlers follow-up) — a separate fake rather than
// growing fakeService, so the non-merchant tests in server_test.go keep
// proving the "unimplemented when the service doesn't support it" branch.
type merchantFakeService struct {
	fakeService
	createFn func(context.Context, uuid.UUID, string, string, decimal.Decimal, string) (model.TopupIntent, error)
	getFn    func(context.Context, uuid.UUID, uuid.UUID) (model.TopupIntent, error)
}

func (f *merchantFakeService) CreateMerchantTopupIntent(ctx context.Context, tenantID uuid.UUID, environment, currency string, amount decimal.Decimal, downstreamKey string) (model.TopupIntent, error) {
	return f.createFn(ctx, tenantID, environment, currency, amount, downstreamKey)
}

func (f *merchantFakeService) GetMerchantTopupIntent(ctx context.Context, tenantID, id uuid.UUID) (model.TopupIntent, error) {
	return f.getFn(ctx, tenantID, id)
}

func merchantTestClient(t *testing.T, service Service, notFound, noVendorAvailable, sandboxVendorUnavailable error) payinv1.PayinServiceClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	payinv1.RegisterPayinServiceServer(server, New(service, notFound, errors.New("no route"), noVendorAvailable, errors.New("screening dependency unavailable"), sandboxVendorUnavailable))
	go func() { _ = server.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close(); server.Stop(); _ = listener.Close() })
	return payinv1.NewPayinServiceClient(connection)
}

func TestCreateMerchantTopupIntent_Success(t *testing.T) {
	tenantID := uuid.New()
	intentID := uuid.New()
	service := &merchantFakeService{createFn: func(_ context.Context, gotTenant uuid.UUID, environment, currency string, amount decimal.Decimal, downstreamKey string) (model.TopupIntent, error) {
		assert.Equal(t, tenantID, gotTenant)
		assert.Equal(t, "live", environment)
		assert.Equal(t, "IDR", currency)
		assert.True(t, amount.Equal(decimal.NewFromInt(50000)))
		assert.Equal(t, "dkey-1", downstreamKey)
		return model.TopupIntent{ID: intentID, MerchantTenantID: tenantID, Vendor: "mockvendor", Currency: currency, Amount: amount, Status: model.TopupStatusPending}, nil
	}}
	client := merchantTestClient(t, service, errors.New("not found"), errors.New("no vendor"), errors.New("sandbox unavailable"))

	resp, err := client.CreateMerchantTopupIntent(context.Background(), &payinv1.CreateMerchantTopupIntentRequest{
		TenantId: tenantID.String(), Environment: "live", Currency: "IDR", Amount: "50000", DownstreamKey: "dkey-1",
	})
	require.NoError(t, err)
	assert.Equal(t, intentID.String(), resp.GetIntent().GetId())
	assert.Equal(t, "mockvendor", resp.GetIntent().GetVendor())
}

func TestCreateMerchantTopupIntent_MissingDownstreamKey_InvalidArgument(t *testing.T) {
	service := &merchantFakeService{}
	client := merchantTestClient(t, service, errors.New("not found"), errors.New("no vendor"), errors.New("sandbox unavailable"))

	_, err := client.CreateMerchantTopupIntent(context.Background(), &payinv1.CreateMerchantTopupIntentRequest{
		TenantId: uuid.NewString(), Environment: "live", Currency: "IDR", Amount: "50000",
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateMerchantTopupIntent_SandboxVendorUnavailable_MapsToFailedPrecondition(t *testing.T) {
	sandboxErr := errors.New("sandbox unavailable")
	service := &merchantFakeService{createFn: func(context.Context, uuid.UUID, string, string, decimal.Decimal, string) (model.TopupIntent, error) {
		return model.TopupIntent{}, sandboxErr
	}}
	client := merchantTestClient(t, service, errors.New("not found"), errors.New("no vendor"), sandboxErr)

	_, err := client.CreateMerchantTopupIntent(context.Background(), &payinv1.CreateMerchantTopupIntentRequest{
		TenantId: uuid.NewString(), Environment: "sandbox", Currency: "IDR", Amount: "50000", DownstreamKey: "dkey-1",
	})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestGetMerchantTopupIntent_NotFound(t *testing.T) {
	notFound := errors.New("not found")
	service := &merchantFakeService{getFn: func(context.Context, uuid.UUID, uuid.UUID) (model.TopupIntent, error) {
		return model.TopupIntent{}, notFound
	}}
	client := merchantTestClient(t, service, notFound, errors.New("no vendor"), errors.New("sandbox unavailable"))

	_, err := client.GetMerchantTopupIntent(context.Background(), &payinv1.GetMerchantTopupIntentRequest{TenantId: uuid.NewString(), Id: uuid.NewString()})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetMerchantTopupIntent_Success(t *testing.T) {
	tenantID := uuid.New()
	id := uuid.New()
	service := &merchantFakeService{getFn: func(_ context.Context, gotTenant, gotID uuid.UUID) (model.TopupIntent, error) {
		assert.Equal(t, tenantID, gotTenant)
		assert.Equal(t, id, gotID)
		return model.TopupIntent{ID: id, MerchantTenantID: tenantID, Vendor: "mockvendor", Status: model.TopupStatusSettled}, nil
	}}
	client := merchantTestClient(t, service, errors.New("not found"), errors.New("no vendor"), errors.New("sandbox unavailable"))

	resp, err := client.GetMerchantTopupIntent(context.Background(), &payinv1.GetMerchantTopupIntentRequest{TenantId: tenantID.String(), Id: id.String()})
	require.NoError(t, err)
	assert.Equal(t, id.String(), resp.GetIntent().GetId())
}

// TestCreateMerchantTopupIntent_ServiceWithoutMerchantSupport_Unimplemented
// proves the type-assertion fallback: a Service that only implements the
// core (non-merchant) contract must fail closed with Unimplemented, never
// panic or silently no-op.
func TestCreateMerchantTopupIntent_ServiceWithoutMerchantSupport_Unimplemented(t *testing.T) {
	service := &fakeService{}
	client := merchantTestClient(t, service, errors.New("not found"), errors.New("no vendor"), errors.New("sandbox unavailable"))

	_, err := client.CreateMerchantTopupIntent(context.Background(), &payinv1.CreateMerchantTopupIntentRequest{
		TenantId: uuid.NewString(), Environment: "live", Currency: "IDR", Amount: "50000", DownstreamKey: "dkey-1",
	})
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}
