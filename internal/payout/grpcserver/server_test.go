package grpcserver

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
)

type fakeService struct {
	created     model.PayoutRequest
	createErr   error
	notFound    error
	destination []byte
	createdBy   string
}

func (f *fakeService) Create(_ context.Context, userID uuid.UUID, amount decimal.Decimal, destination []byte, createdBy, quoteID string) (uuid.UUID, error) {
	f.destination = append([]byte(nil), destination...)
	f.createdBy = createdBy
	if f.createErr != nil {
		return uuid.Nil, f.createErr
	}
	f.created.UserID = userID
	f.created.Amount = amount
	return f.created.ID, nil
}
func (f *fakeService) Get(_ context.Context, id uuid.UUID) (model.PayoutRequest, error) {
	if f.notFound != nil || id != f.created.ID {
		return model.PayoutRequest{}, f.notFound
	}
	return f.created, nil
}

func testClient(t *testing.T, service *fakeService, notFound error) payoutv1.PayoutServiceClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	payoutv1.RegisterPayoutServiceServer(server, New(service, notFound, errors.New("no route"), errors.New("screening blocked")))
	go func() { _ = server.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close(); server.Stop(); _ = listener.Close() })
	return payoutv1.NewPayoutServiceClient(connection)
}

func TestCreatePayoutSuccessPreservesDestination(t *testing.T) {
	now := time.Now()
	service := &fakeService{created: model.PayoutRequest{ID: uuid.New(), Currency: "IDR", Vendor: "mockvendor", Status: model.StatusSettled, CreatedAt: now, UpdatedAt: now}}
	client := testClient(t, service, errors.New("not found"))
	userID := uuid.New()
	raw := []byte(`{"account_no":"001"}`)
	result, err := client.CreatePayout(context.Background(), &payoutv1.CreatePayoutRequest{UserId: userID.String(), Amount: "50000", Destination: raw, CreatedBy: "gateway-user"})
	require.NoError(t, err)
	assert.Equal(t, service.created.ID.String(), result.GetPayout().GetId())
	assert.Equal(t, raw, service.destination)
	assert.Equal(t, "gateway-user", service.createdBy)
}

func TestCreatePayoutInsufficientFunds(t *testing.T) {
	service := &fakeService{createErr: &ledgererr.LedgerError{Code: "INSUFFICIENT_FUNDS", Message: "balance too low"}}
	_, err := testClient(t, service, errors.New("not found")).CreatePayout(context.Background(), &payoutv1.CreatePayoutRequest{UserId: uuid.NewString(), Amount: "50000", Destination: []byte(`{}`)})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestGetPayoutOwnerAndNonOwner(t *testing.T) {
	notFound := errors.New("not found")
	owner := uuid.New()
	service := &fakeService{created: model.PayoutRequest{ID: uuid.New(), UserID: owner, Amount: decimal.NewFromInt(1)}}
	client := testClient(t, service, notFound)
	_, err := client.GetPayout(context.Background(), &payoutv1.GetPayoutRequest{Id: service.created.ID.String(), UserId: owner.String()})
	require.NoError(t, err)
	_, err = client.GetPayout(context.Background(), &payoutv1.GetPayoutRequest{Id: service.created.ID.String(), UserId: uuid.NewString()})
	assert.Equal(t, codes.NotFound, status.Code(err))
}
