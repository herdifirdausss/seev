package grpcserver

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
	"google.golang.org/protobuf/types/known/structpb"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

type fakeService struct {
	postCommand  processors.Command
	postErr      error
	tx           model.LedgerTransaction
	txErr        error
	currency     string
	currencyErr  error
	fee          decimal.Decimal
	feeGateway   string
	feeOK        bool
	provisionID  uuid.UUID
	provisionCCY string
	provisionErr error
	quoteFee     decimal.Decimal
	quoteGateway string
	quoteErr     error
	kycUserID    uuid.UUID
	kycLevel     int32
	kycErr       error
}

func (f *fakeService) Post(_ context.Context, command processors.Command) error {
	f.postCommand = command
	return f.postErr
}
func (f *fakeService) GetTransactionByIdempotencyKey(context.Context, string, string) (model.LedgerTransaction, error) {
	return f.tx, f.txErr
}
func (f *fakeService) GetUserCurrency(context.Context, uuid.UUID, string) (string, error) {
	return f.currency, f.currencyErr
}
func (f *fakeService) ResolveFee(context.Context, uuid.UUID, string, string, string, decimal.Decimal) (decimal.Decimal, string, bool) {
	return f.fee, f.feeGateway, f.feeOK
}
func (f *fakeService) ProvisionUser(_ context.Context, userID uuid.UUID, currency string) ([]model.Account, error) {
	f.provisionID, f.provisionCCY = userID, currency
	return nil, f.provisionErr
}
func (f *fakeService) ConsumeFeeQuote(context.Context, uuid.UUID, uuid.UUID, string, string, decimal.Decimal, string) (decimal.Decimal, string, error) {
	return f.quoteFee, f.quoteGateway, f.quoteErr
}
func (f *fakeService) ApplyKycTier(_ context.Context, userID uuid.UUID, kycLevel int32) error {
	f.kycUserID, f.kycLevel = userID, kycLevel
	return f.kycErr
}

func newTestClient(t *testing.T, service Service) ledgerv1.LedgerServiceClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	ledgerv1.RegisterLedgerServiceServer(server, New(service))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	t.Cleanup(func() { _ = listener.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	conn, err := grpc.DialContext(ctx, "bufnet", //nolint:staticcheck // bufconn test requires a blocking custom dialer.
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(), //nolint:staticcheck
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return ledgerv1.NewLedgerServiceClient(conn)
}

func TestServerHappyPath(t *testing.T) {
	userID, targetID, referenceID := uuid.New(), uuid.New(), uuid.New()
	txID, sourceID := uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	service := &fakeService{
		tx: model.LedgerTransaction{
			ID: txID, IdempotencyKey: "idem", IdempotencyScope: "scope", Type: "money_in",
			Status: "posted", Amount: decimal.NewFromInt(100), Currency: "IDR",
			SourceAccountID: sourceID, CreatedAt: now, UpdatedAt: now,
		},
		currency: "IDR", fee: decimal.NewFromInt(5), feeGateway: "platform", feeOK: true,
	}
	client := newTestClient(t, service)
	metadata, err := structpb.NewStruct(map[string]any{"gateway": "bca", "attempt": float64(1)})
	require.NoError(t, err)

	_, err = client.Post(context.Background(), &ledgerv1.PostRequest{
		IdempotencyKey: "idem", IdempotencyScope: "scope", Type: "money_in", Amount: "100",
		UserId: userID.String(), TargetUserId: targetID.String(), PocketCode: "daily",
		ReferenceId: referenceID.String(), Metadata: metadata,
	})
	require.NoError(t, err)
	require.Equal(t, decimal.NewFromInt(100), service.postCommand.Amount)
	require.Equal(t, userID, service.postCommand.UserID)
	require.Equal(t, targetID, service.postCommand.TargetUserID)
	require.Equal(t, referenceID, service.postCommand.ReferenceID)
	require.Equal(t, "bca", service.postCommand.Metadata["gateway"])

	tx, err := client.GetTransactionByIdempotencyKey(context.Background(), &ledgerv1.GetTxByIdemKeyRequest{IdempotencyKey: "idem", IdempotencyScope: "scope"})
	require.NoError(t, err)
	require.Equal(t, txID.String(), tx.Id)
	require.Equal(t, "100", tx.Amount)
	require.Equal(t, sourceID.String(), tx.SourceAccountId)
	require.Empty(t, tx.DestinationAccountId)
	require.True(t, tx.CreatedAt.AsTime().Equal(now))

	currency, err := client.GetUserCurrency(context.Background(), &ledgerv1.GetUserCurrencyRequest{UserId: userID.String()})
	require.NoError(t, err)
	require.Equal(t, "IDR", currency.Currency)

	fee, err := client.ResolveFee(context.Background(), &ledgerv1.ResolveFeeRequest{Type: "money_in", Gateway: "bca", Currency: "IDR", Amount: "100"})
	require.NoError(t, err)
	require.Equal(t, "5", fee.Fee)
	require.Equal(t, "platform", fee.FeeGateway)
	require.True(t, fee.Ok)

	_, err = client.ProvisionUser(context.Background(), &ledgerv1.ProvisionUserRequest{UserId: userID.String(), Currency: "IDR"})
	require.NoError(t, err)
	require.Equal(t, userID, service.provisionID)
	require.Equal(t, "IDR", service.provisionCCY)
}

// TestServerPost_InjectsRequestIDFromCtx proves docs/plan/36 Task T5: the
// Post RPC stamps metadata["request_id"] from the gRPC ctx (populated by
// pkg/grpcx's server interceptor, docs/plan/36 Task T3) onto the Command it
// forwards to the service — regardless of what the caller's own proto
// Metadata said (payin/payout are trusted callers, but the trace id must
// still be the server's own ctx-derived value, not something they set).
func TestServerPost_InjectsRequestIDFromCtx(t *testing.T) {
	userID := uuid.New()
	service := &fakeService{}
	server := New(service)

	metadata, err := structpb.NewStruct(map[string]any{"request_id": "caller-supplied-should-be-overwritten"})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), middleware.RequestIDKey, "trace-from-grpc-ctx")
	_, err = server.Post(ctx, &ledgerv1.PostRequest{
		IdempotencyKey: "idem", IdempotencyScope: "scope", Type: "money_in", Amount: "100",
		UserId: userID.String(), Metadata: metadata,
	})
	require.NoError(t, err)
	require.Equal(t, "trace-from-grpc-ctx", service.postCommand.Metadata["request_id"])
}

func TestServerErrorMapping(t *testing.T) {
	service := &fakeService{}
	client := newTestClient(t, service)
	request := &ledgerv1.PostRequest{Amount: "1"}

	tests := []struct {
		name   string
		err    error
		code   codes.Code
		reason string
	}{
		{name: "ledger error", err: apperror.NewBizErr(apperror.ErrInsufficientFunds, "balance too low"), code: codes.FailedPrecondition, reason: "INSUFFICIENT_FUNDS"},
		{name: "already closed", err: apperror.NewBizErr(apperror.ErrAlreadyClosed, "race lost"), code: codes.Aborted, reason: ledgererr.ReasonAlreadyClosed},
		{name: "not found", err: apperror.ErrTransactionNotFound, code: codes.NotFound},
		{name: "internal", err: errors.New("database unavailable"), code: codes.Internal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service.postErr = test.err
			_, err := client.Post(context.Background(), request)
			require.Equal(t, test.code, status.Code(err))
			if test.reason != "" {
				st, _ := status.FromError(err)
				var info *errdetails.ErrorInfo
				for _, detail := range st.Details() {
					if candidate, ok := detail.(*errdetails.ErrorInfo); ok {
						info = candidate
					}
				}
				require.NotNil(t, info)
				require.Equal(t, ledgererr.DomainLedger, info.Domain)
				require.Equal(t, test.reason, info.Reason)
			}
		})
	}
}

func TestServerRejectsInvalidWireValues(t *testing.T) {
	client := newTestClient(t, &fakeService{})
	_, err := client.Post(context.Background(), &ledgerv1.PostRequest{Amount: "1.5"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = client.Post(context.Background(), &ledgerv1.PostRequest{Amount: "one"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = client.Post(context.Background(), &ledgerv1.PostRequest{Amount: "1", UserId: "not-a-uuid"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
