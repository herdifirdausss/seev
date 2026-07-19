// Package grpcserver exposes the ledger facade over its internal gRPC contract.
package grpcserver

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

// Service is the subset of the ledger facade exposed over gRPC.
type Service interface {
	Post(context.Context, processors.Command) error
	GetTransactionByIdempotencyKey(context.Context, string, string) (model.LedgerTransaction, error)
	GetUserCurrency(context.Context, uuid.UUID, string) (string, error)
	ResolveFee(context.Context, uuid.UUID, string, string, string, decimal.Decimal) (decimal.Decimal, string, bool)
	ProvisionUser(context.Context, uuid.UUID, string) ([]model.Account, error)
	// ConsumeFeeQuote is docs/plan/38 Task T5's additive RPC backing —
	// returns *apperror.LedgerError (Code "QUOTE_EXPIRED"/"QUOTE_MISMATCH")
	// on rejection.
	ConsumeFeeQuote(ctx context.Context, quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, ref string) (fee decimal.Decimal, feeGateway string, err error)
	// ApplyKycTier is docs/plan/39 Task T5's additive RPC backing — returns
	// apperror.ErrUnknownKycTier for an unrecognized kyc_level.
	ApplyKycTier(ctx context.Context, userID uuid.UUID, kycLevel int32) error
}

type Server struct {
	ledgerv1.UnimplementedLedgerServiceServer
	service Service
}

func New(service Service) *Server { return &Server{service: service} }

func (s *Server) Post(ctx context.Context, req *ledgerv1.PostRequest) (*ledgerv1.PostResponse, error) {
	amount, err := integralAmount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	userID, err := parseUUID(req.GetUserId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "user_id: %v", err)
	}
	targetUserID, err := parseUUID(req.GetTargetUserId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "target_user_id: %v", err)
	}
	referenceID, err := parseUUID(req.GetReferenceId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "reference_id: %v", err)
	}
	var metadata map[string]any
	if req.Metadata != nil {
		metadata = req.Metadata.AsMap()
	}
	// request_id sourced from the gRPC ctx (populated by pkg/grpcx's server
	// interceptor from the x-request-id metadata the caller's client
	// interceptor set, docs/plan/36 Task T3) — this is a freshly converted
	// map (AsMap above), not a reference the caller can observe, so setting
	// it here can't leak into or mutate the caller's own request object.
	if id := middleware.RequestIDFromCtx(ctx); id != "" {
		if metadata == nil {
			metadata = make(map[string]any, 1)
		}
		metadata["request_id"] = id
	}
	err = s.service.Post(ctx, processors.Command{
		IdempotencyKey: req.GetIdempotencyKey(), IdempotencyScope: req.GetIdempotencyScope(),
		Type: req.GetType(), Amount: amount, UserID: userID, TargetUserID: targetUserID,
		PocketCode: req.GetPocketCode(), ReferenceID: referenceID, Metadata: metadata,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &ledgerv1.PostResponse{}, nil
}

func (s *Server) GetTransactionByIdempotencyKey(ctx context.Context, req *ledgerv1.GetTxByIdemKeyRequest) (*ledgerv1.Transaction, error) {
	tx, err := s.service.GetTransactionByIdempotencyKey(ctx, req.GetIdempotencyKey(), req.GetIdempotencyScope())
	if err != nil {
		return nil, mapError(err)
	}
	return transactionToProto(tx), nil
}

func (s *Server) GetUserCurrency(ctx context.Context, req *ledgerv1.GetUserCurrencyRequest) (*ledgerv1.GetUserCurrencyResponse, error) {
	userID, err := parseUUID(req.GetUserId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "user_id: %v", err)
	}
	currency, err := s.service.GetUserCurrency(ctx, userID, req.GetPocketCode())
	if err != nil {
		return nil, mapError(err)
	}
	return &ledgerv1.GetUserCurrencyResponse{Currency: currency}, nil
}

func (s *Server) ResolveFee(ctx context.Context, req *ledgerv1.ResolveFeeRequest) (*ledgerv1.ResolveFeeResponse, error) {
	amount, err := integralAmount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	userID, err := parseUUID(req.GetUserId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "user_id: %v", err)
	}
	fee, gateway, ok := s.service.ResolveFee(ctx, userID, req.GetType(), req.GetGateway(), req.GetCurrency(), amount)
	return &ledgerv1.ResolveFeeResponse{Fee: fee.String(), FeeGateway: gateway, Ok: ok}, nil
}

// ConsumeFeeQuote serves the additive RPC backing docs/plan/38 Task T5.
// Service.ConsumeFeeQuote already returns *apperror.LedgerError (Code
// "QUOTE_EXPIRED"/"QUOTE_MISMATCH") on rejection — the EXISTING generic
// mapError handles that exactly like any other business error, so
// pkg/ledgererr.FromStatus decodes it on the caller side with zero new
// ledgererr code (same free "gRPC parity" already established for
// SCREENING_BLOCKED and reused by QUOTE_EXPIRED/QUOTE_MISMATCH at the HTTP
// layer too, docs/plan/38 Task T4's own Hasil).
func (s *Server) ConsumeFeeQuote(ctx context.Context, req *ledgerv1.ConsumeFeeQuoteRequest) (*ledgerv1.ConsumeFeeQuoteResponse, error) {
	amount, err := integralAmount(req.GetAmount())
	if err != nil {
		return nil, err
	}
	quoteID, err := parseUUID(req.GetQuoteId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "quote_id: %v", err)
	}
	userID, err := parseUUID(req.GetUserId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "user_id: %v", err)
	}
	fee, feeGateway, callErr := s.service.ConsumeFeeQuote(ctx, quoteID, userID, req.GetTransactionType(), req.GetCurrency(), amount, req.GetConsumedByRef())
	if callErr != nil {
		return nil, mapError(callErr)
	}
	return &ledgerv1.ConsumeFeeQuoteResponse{FeeAmount: fee.String(), FeeGateway: feeGateway}, nil
}

// ApplyKycTier serves the additive RPC backing docs/plan/39 Task T5. An
// unrecognized kyc_level is a caller input error (InvalidArgument), handled
// explicitly here rather than through the generic mapError (which reserves
// FailedPrecondition for business-state failures, not bad input) — every
// other error still goes through mapError for consistency.
func (s *Server) ApplyKycTier(ctx context.Context, req *ledgerv1.ApplyKycTierRequest) (*ledgerv1.ApplyKycTierResponse, error) {
	userID, err := parseUUID(req.GetUserId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "user_id: %v", err)
	}
	if callErr := s.service.ApplyKycTier(ctx, userID, req.GetKycLevel()); callErr != nil {
		if errors.Is(callErr, apperror.ErrUnknownKycTier) {
			return nil, status.Errorf(codes.InvalidArgument, "unknown kyc_level: %d", req.GetKycLevel())
		}
		return nil, mapError(callErr)
	}
	return &ledgerv1.ApplyKycTierResponse{}, nil
}

func (s *Server) BatchGetAssuranceTransactions(ctx context.Context, req *ledgerv1.BatchGetAssuranceTransactionsRequest) (*ledgerv1.BatchGetAssuranceTransactionsResponse, error) {
	if len(req.GetSelectors()) > 500 || len(req.GetFeeQuoteIds()) > 500 {
		return nil, status.Error(codes.InvalidArgument, "assurance batch limit is 500")
	}
	reader, ok := s.service.(interface {
		BatchGetAssuranceTransactions(context.Context, *ledgerv1.BatchGetAssuranceTransactionsRequest) (*ledgerv1.BatchGetAssuranceTransactionsResponse, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "ledger assurance projection unavailable")
	}
	return reader.BatchGetAssuranceTransactions(ctx, req)
}

func (s *Server) ProvisionUser(ctx context.Context, req *ledgerv1.ProvisionUserRequest) (*ledgerv1.ProvisionUserResponse, error) {
	userID, err := parseUUID(req.GetUserId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "user_id: %v", err)
	}
	if _, err := s.service.ProvisionUser(ctx, userID, req.GetCurrency()); err != nil {
		return nil, mapError(err)
	}
	return &ledgerv1.ProvisionUserResponse{}, nil
}

func integralAmount(value string) (decimal.Decimal, error) {
	amount, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Zero, status.Error(codes.InvalidArgument, "amount must be a decimal string")
	}
	if !amount.Equal(amount.Truncate(0)) {
		return decimal.Zero, status.Error(codes.InvalidArgument, "amount must be an integer minor-unit value")
	}
	return amount, nil
}

func parseUUID(value string) (uuid.UUID, error) {
	if value == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(value)
}

func uuidString(value uuid.UUID) string {
	if value == uuid.Nil {
		return ""
	}
	return value.String()
}

func transactionToProto(tx model.LedgerTransaction) *ledgerv1.Transaction {
	return &ledgerv1.Transaction{
		Id: uuidString(tx.ID), IdempotencyKey: tx.IdempotencyKey, IdempotencyScope: tx.IdempotencyScope,
		Type: tx.Type, Status: tx.Status, Amount: tx.Amount.String(), Currency: tx.Currency,
		SourceAccountId: uuidString(tx.SourceAccountID), DestinationAccountId: uuidString(tx.DestinationAccountID),
		ErrorMessage: tx.ErrorMessage, ExternalRef: tx.ExternalRef, Gateway: tx.Gateway,
		CreatedAt: timestamppb.New(tx.CreatedAt), UpdatedAt: timestamppb.New(tx.UpdatedAt),
	}
}

func mapError(err error) error {
	if errors.Is(err, apperror.ErrAlreadyClosed) {
		return statusWithInfo(codes.Aborted, err.Error(), ledgererr.ReasonAlreadyClosed)
	}
	var ledgerError *apperror.LedgerError
	if errors.As(err, &ledgerError) {
		return statusWithInfo(codes.FailedPrecondition, ledgerError.Message, ledgerError.Code)
	}
	if isNotFound(err) {
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.Internal, "internal ledger error")
}

func statusWithInfo(code codes.Code, message, reason string) error {
	st, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: ledgererr.DomainLedger})
	if err != nil {
		return status.Error(codes.Internal, "internal ledger error")
	}
	return st.Err()
}

func isNotFound(err error) bool {
	return errors.Is(err, apperror.ErrAccountNotFound) ||
		errors.Is(err, apperror.ErrTransactionNotFound) ||
		errors.Is(err, apperror.ErrOriginalNotFound) ||
		errors.Is(err, apperror.ErrOutboxEventNotFound) ||
		errors.Is(err, apperror.ErrPendingAdjustmentNotFound) ||
		errors.Is(err, apperror.ErrReconBatchNotFound) ||
		errors.Is(err, apperror.ErrReconItemNotFound) ||
		errors.Is(err, apperror.ErrScheduledTransactionNotFound)
}
