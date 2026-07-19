package grpcserver

import (
	"context"
	"errors"
	"fmt"
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

	fraudv1 "github.com/herdifirdausss/seev/gen/fraud/v1"
	"github.com/herdifirdausss/seev/internal/fraud/model"
)

type serviceStub struct {
	input   model.ScreenInput
	verdict model.Verdict
	err     error
}

func (s *serviceStub) Screen(_ context.Context, input model.ScreenInput) (model.Verdict, error) {
	s.input = input
	return s.verdict, s.err
}

func testClient(t *testing.T, service Service) fraudv1.FraudServiceClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	fraudv1.RegisterFraudServiceServer(server, New(service))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	conn, err := grpc.DialContext(ctx, "bufnet", //nolint:staticcheck // bufconn requires legacy blocking dial.
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(), //nolint:staticcheck
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return fraudv1.NewFraudServiceClient(conn)
}

func TestScreenRoundTrip(t *testing.T) {
	service := &serviceStub{verdict: model.Verdict{Block: true, Reason: "threshold"}}
	client := testClient(t, service)
	userID := uuid.New()
	response, err := client.Screen(context.Background(), &fraudv1.ScreenRequest{
		TxType: "transfer_p2p", UserId: userID.String(), Amount: "100000", Currency: "IDR",
	})
	require.NoError(t, err)
	assert.True(t, response.GetBlock())
	assert.Equal(t, "threshold", response.GetReason())
	assert.Equal(t, userID, service.input.UserID)
	assert.Equal(t, "100000", service.input.Amount.String())
}

// TestScreenPropagatesRequestIDAndFlow proves docs/plan/37 Task T1: the
// additive request_id/flow fields on ScreenRequest reach the service's
// ScreenInput unchanged, for persistence into screening_events.
func TestScreenPropagatesRequestIDAndFlow(t *testing.T) {
	service := &serviceStub{}
	client := testClient(t, service)
	userID := uuid.New()
	_, err := client.Screen(context.Background(), &fraudv1.ScreenRequest{
		TxType: "transfer_p2p", UserId: userID.String(), Amount: "100000", Currency: "IDR",
		RequestId: "trace-abc-123", Flow: "p2p_transfer",
	})
	require.NoError(t, err)
	assert.Equal(t, "trace-abc-123", service.input.RequestID)
	assert.Equal(t, "p2p_transfer", service.input.Flow)
}

func TestScreenRejectsInvalidInput(t *testing.T) {
	client := testClient(t, &serviceStub{})
	_, err := client.Screen(context.Background(), &fraudv1.ScreenRequest{TxType: "transfer_p2p", UserId: "bad", Amount: "1", Currency: "IDR"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestScreenDependencyUnavailable_MapsToDistinguishableStatus proves
// docs/plan/45 Task T3/K4: Module.Screen returning
// model.ErrDependencyUnavailable (wrapped, as VelocityAnomalyRule.Screen
// actually does) maps to codes.FailedPrecondition with the exact
// "DEPENDENCY_UNAVAILABLE" message pkg/fraudcheck.Client checks for —
// distinct from every OTHER Screen error, which stays codes.Internal.
func TestScreenDependencyUnavailable_MapsToDistinguishableStatus(t *testing.T) {
	service := &serviceStub{err: fmt.Errorf("velocity counter: %w", model.ErrDependencyUnavailable)}
	client := testClient(t, service)

	_, err := client.Screen(context.Background(), &fraudv1.ScreenRequest{
		TxType: "transfer_p2p", UserId: uuid.New().String(), Amount: "100000", Currency: "IDR",
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Equal(t, dependencyUnavailableMessage, status.Convert(err).Message())
}

// TestScreenGenericError_StaysInternal proves an unrelated Screen error
// (not the dependency-unavailable sentinel) still maps to codes.Internal,
// unchanged from before this track.
func TestScreenGenericError_StaysInternal(t *testing.T) {
	service := &serviceStub{err: errors.New("database exploded")}
	client := testClient(t, service)

	_, err := client.Screen(context.Background(), &fraudv1.ScreenRequest{
		TxType: "transfer_p2p", UserId: uuid.New().String(), Amount: "100000", Currency: "IDR",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}
