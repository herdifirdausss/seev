package ledgerclient

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
)

type fakeLedgerServer struct {
	ledgerv1.UnimplementedLedgerServiceServer
	lastPost      *ledgerv1.PostRequest
	postErr       error
	provisionUser string
}

func (s *fakeLedgerServer) Post(_ context.Context, request *ledgerv1.PostRequest) (*ledgerv1.PostResponse, error) {
	s.lastPost = request
	return &ledgerv1.PostResponse{}, s.postErr
}
func (s *fakeLedgerServer) GetTransactionByIdempotencyKey(context.Context, *ledgerv1.GetTxByIdemKeyRequest) (*ledgerv1.Transaction, error) {
	now := timestamppb.New(time.Unix(123, 0))
	return &ledgerv1.Transaction{
		Id:             uuid.MustParse("00000000-0000-0000-0000-000000000101").String(),
		IdempotencyKey: "idem", IdempotencyScope: "scope", Type: "money_in", Status: "posted",
		Amount: "123", Currency: "IDR", SourceAccountId: "", DestinationAccountId: "",
		ExternalRef: "ext", Gateway: "bca", CreatedAt: now, UpdatedAt: now,
	}, nil
}
func (s *fakeLedgerServer) GetUserCurrency(context.Context, *ledgerv1.GetUserCurrencyRequest) (*ledgerv1.GetUserCurrencyResponse, error) {
	return &ledgerv1.GetUserCurrencyResponse{Currency: "IDR"}, nil
}
func (s *fakeLedgerServer) ResolveFee(context.Context, *ledgerv1.ResolveFeeRequest) (*ledgerv1.ResolveFeeResponse, error) {
	return &ledgerv1.ResolveFeeResponse{Fee: "7", FeeGateway: "platform", Ok: true}, nil
}
func (s *fakeLedgerServer) ProvisionUser(_ context.Context, request *ledgerv1.ProvisionUserRequest) (*ledgerv1.ProvisionUserResponse, error) {
	s.provisionUser = request.GetUserId()
	return &ledgerv1.ProvisionUserResponse{}, nil
}

func newClient(t *testing.T, serverImpl ledgerv1.LedgerServiceServer) *Client {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	ledgerv1.RegisterLedgerServiceServer(server, serverImpl)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	conn, err := grpc.DialContext(ctx, "bufnet", //nolint:staticcheck // bufconn requires blocking custom dialing.
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(), //nolint:staticcheck
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return New(conn)
}

func TestClientWireRoundTrip(t *testing.T) {
	server := &fakeLedgerServer{}
	client := newClient(t, server)
	userID, targetID, referenceID := uuid.New(), uuid.New(), uuid.New()
	err := client.Post(context.Background(), Command{
		IdempotencyKey: "idem", IdempotencyScope: "scope", Type: "transfer_p2p",
		Amount: decimal.NewFromInt(123), UserID: userID, TargetUserID: targetID,
		ReferenceID: referenceID, Metadata: map[string]any{"gateway": "bca"},
	})
	require.NoError(t, err)
	require.Equal(t, "123", server.lastPost.Amount)
	require.Equal(t, userID.String(), server.lastPost.UserId)
	require.Equal(t, targetID.String(), server.lastPost.TargetUserId)
	require.Equal(t, referenceID.String(), server.lastPost.ReferenceId)
	require.Equal(t, "bca", server.lastPost.Metadata.AsMap()["gateway"])

	tx, err := client.GetTransactionByIdempotencyKey(context.Background(), "idem", "scope")
	require.NoError(t, err)
	require.Equal(t, decimal.NewFromInt(123), tx.Amount)
	require.Equal(t, "posted", tx.Status)
	require.True(t, time.Unix(123, 0).Equal(tx.CreatedAt))

	currency, err := client.GetUserCurrency(context.Background(), userID, "")
	require.NoError(t, err)
	require.Equal(t, "IDR", currency)
	fee, gateway, ok, err := client.ResolveFee(context.Background(), userID, "transfer_p2p", "", "IDR", decimal.NewFromInt(100))
	require.NoError(t, err)
	require.Equal(t, decimal.NewFromInt(7), fee)
	require.Equal(t, "platform", gateway)
	require.True(t, ok)
	require.NoError(t, client.ProvisionUser(context.Background(), userID, "IDR"))
	require.Equal(t, userID.String(), server.provisionUser)
}

func TestClientReconstructsLedgerErrors(t *testing.T) {
	server := &fakeLedgerServer{}
	client := newClient(t, server)

	server.postErr = statusInfo(t, codes.FailedPrecondition, "balance too low", "INSUFFICIENT_FUNDS")
	err := client.Post(context.Background(), Command{Amount: decimal.NewFromInt(1)})
	var ledgerError *ledgererr.LedgerError
	require.ErrorAs(t, err, &ledgerError)
	require.Equal(t, "INSUFFICIENT_FUNDS", ledgerError.Code)

	server.postErr = statusInfo(t, codes.Aborted, "race lost", ledgererr.ReasonAlreadyClosed)
	err = client.Post(context.Background(), Command{Amount: decimal.NewFromInt(1)})
	require.True(t, errors.Is(err, ledgererr.ErrAlreadyClosed))
}

func statusInfo(t *testing.T, code codes.Code, message, reason string) error {
	t.Helper()
	st, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: ledgererr.DomainLedger})
	require.NoError(t, err)
	return st.Err()
}

func TestClientRejectsMalformedResponsesAndMetadata(t *testing.T) {
	client := newClient(t, &malformedLedgerServer{})
	_, err := client.GetTransactionByIdempotencyKey(context.Background(), "idem", "")
	require.ErrorContains(t, err, "invalid transaction id")
	err = client.Post(context.Background(), Command{Amount: decimal.NewFromInt(1), Metadata: map[string]any{"bad": make(chan int)}})
	require.ErrorContains(t, err, "encode metadata")
}

type malformedLedgerServer struct {
	ledgerv1.UnimplementedLedgerServiceServer
}

func (*malformedLedgerServer) GetTransactionByIdempotencyKey(context.Context, *ledgerv1.GetTxByIdemKeyRequest) (*ledgerv1.Transaction, error) {
	return &ledgerv1.Transaction{Id: "bad", Amount: "bad"}, nil
}
