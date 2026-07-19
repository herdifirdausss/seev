package grpcserver

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	fraudv1 "github.com/herdifirdausss/seev/gen/fraud/v1"
	"github.com/herdifirdausss/seev/internal/fraud/model"
)

// dependencyUnavailableMessage is the exact gRPC status message
// pkg/fraudcheck.Client checks for (docs/plan/45 Task T3/K4) to
// distinguish "fraud-service is reachable but its velocity dependency is
// down" from a genuine transport failure — codes.FailedPrecondition alone
// isn't enough of a signal to rely on in isolation (some future addition to
// this RPC could reuse the same code for an unrelated reason). This
// literal is intentionally duplicated (not a shared import) in
// pkg/fraudcheck: pkg/ must never import internal/ (PROJECT_GUIDE.md boundary
// rule), and this string is effectively part of the fraudv1 wire contract
// between fraud-service and its callers, not an internal implementation
// detail. Keep both copies in sync if this ever changes.
const dependencyUnavailableMessage = "DEPENDENCY_UNAVAILABLE"

type Service interface {
	Screen(context.Context, model.ScreenInput) (model.Verdict, error)
}

type Server struct {
	fraudv1.UnimplementedFraudServiceServer
	service Service
}

func New(service Service) *Server { return &Server{service: service} }

func (s *Server) Screen(ctx context.Context, request *fraudv1.ScreenRequest) (*fraudv1.ScreenResponse, error) {
	if request.GetTxType() == "" {
		return nil, status.Error(codes.InvalidArgument, "tx_type is required")
	}
	userID, err := uuid.Parse(request.GetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "user_id must be a valid UUID")
	}
	amount, err := decimal.NewFromString(request.GetAmount())
	if err != nil || !amount.IsPositive() || !amount.Equal(amount.Truncate(0)) {
		return nil, status.Error(codes.InvalidArgument, "amount must be a positive integer decimal string")
	}
	if len(request.GetCurrency()) != 3 {
		return nil, status.Error(codes.InvalidArgument, "currency must be a 3-letter code")
	}

	verdict, err := s.service.Screen(ctx, model.ScreenInput{
		TxType: request.GetTxType(), UserID: userID, Amount: amount, Currency: request.GetCurrency(),
		RequestID: request.GetRequestId(), Flow: request.GetFlow(),
	})
	if err != nil {
		if errors.Is(err, model.ErrDependencyUnavailable) {
			return nil, status.Error(codes.FailedPrecondition, dependencyUnavailableMessage)
		}
		return nil, status.Error(codes.Internal, "screening failed")
	}
	return &fraudv1.ScreenResponse{Block: verdict.Block, Reason: verdict.Reason}, nil
}
