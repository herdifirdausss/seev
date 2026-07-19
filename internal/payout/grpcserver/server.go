package grpcserver

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
)

type Service interface {
	Create(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, destination []byte, createdBy, quoteID string) (uuid.UUID, error)
	Get(context.Context, uuid.UUID) (model.PayoutRequest, error)
}

type Server struct {
	payoutv1.UnimplementedPayoutServiceServer
	service                        Service
	notFound                       error
	noRoute                        error
	noVendorAvailable              error
	screeningBlocked               error
	screeningDependencyUnavailable error
}

func New(service Service, notFound, noRoute, noVendorAvailable, screeningBlocked, screeningDependencyUnavailable error) *Server {
	return &Server{
		service: service, notFound: notFound, noRoute: noRoute, noVendorAvailable: noVendorAvailable,
		screeningBlocked: screeningBlocked, screeningDependencyUnavailable: screeningDependencyUnavailable,
	}
}

func (s *Server) CreatePayout(ctx context.Context, request *payoutv1.CreatePayoutRequest) (*payoutv1.CreatePayoutResponse, error) {
	userID, amount, err := parseUserAndAmount(request.GetUserId(), request.GetAmount())
	if err != nil {
		return nil, err
	}
	if len(request.GetDestination()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "destination is required")
	}
	id, callErr := s.service.Create(ctx, userID, amount, request.GetDestination(), request.GetCreatedBy(), request.GetQuoteId())
	if callErr != nil {
		if strings.Contains(callErr.Error(), "intake paused") {
			return nil, status.Error(codes.FailedPrecondition, "INTAKE_PAUSED")
		}
		if errors.Is(callErr, s.noRoute) {
			return nil, status.Error(codes.FailedPrecondition, "no payout route available")
		}
		if errors.Is(callErr, s.noVendorAvailable) {
			// docs/plan/40 Task T2 — distinct gRPC code from "no route"
			// (FailedPrecondition/422): every candidate vendor is
			// registered but circuit-broken, a TRANSIENT condition the
			// caller should retry, not a config problem.
			return nil, status.Error(codes.Unavailable, "no vendor available")
		}
		if errors.Is(callErr, s.screeningBlocked) {
			return nil, status.Error(codes.FailedPrecondition, callErr.Error())
		}
		if errors.Is(callErr, s.screeningDependencyUnavailable) {
			// docs/plan/45 Task T3/K4 — codes.Unavailable like
			// noVendorAvailable above (both transient, retry-worthy), but a
			// DIFFERENT message so the gateway can tell them apart.
			return nil, status.Error(codes.Unavailable, "screening dependency unavailable")
		}
		var business *ledgererr.LedgerError
		if errors.As(callErr, &business) {
			return nil, status.Error(codes.FailedPrecondition, business.Error())
		}
		return nil, status.Error(codes.Internal, "create payout failed")
	}
	value, getErr := s.service.Get(ctx, id)
	if getErr != nil {
		return nil, status.Error(codes.Internal, "read created payout failed")
	}
	return &payoutv1.CreatePayoutResponse{Payout: payoutToProto(value)}, nil
}

func (s *Server) GetPayout(ctx context.Context, request *payoutv1.GetPayoutRequest) (*payoutv1.GetPayoutResponse, error) {
	id, err := uuid.Parse(request.GetId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	userID, err := uuid.Parse(request.GetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "user_id must be a valid UUID")
	}
	value, callErr := s.service.Get(ctx, id)
	if callErr != nil {
		if errors.Is(callErr, s.notFound) {
			return nil, status.Error(codes.NotFound, "payout request not found")
		}
		return nil, status.Error(codes.Internal, "get payout failed")
	}
	if value.UserID != userID {
		return nil, status.Error(codes.NotFound, "payout request not found")
	}
	return &payoutv1.GetPayoutResponse{Payout: payoutToProto(value)}, nil
}

func (s *Server) ListAssuranceRecords(ctx context.Context, request *payoutv1.ListAssuranceRecordsRequest) (*payoutv1.ListAssuranceRecordsResponse, error) {
	reader, ok := s.service.(interface {
		ListAssuranceRecords(context.Context, *payoutv1.ListAssuranceRecordsRequest) (*payoutv1.ListAssuranceRecordsResponse, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "payout assurance projection unavailable")
	}
	return reader.ListAssuranceRecords(ctx, request)
}

func (s *Server) GetIntakeControl(ctx context.Context, request *payoutv1.GetIntakeControlRequest) (*payoutv1.GetIntakeControlResponse, error) {
	reader, ok := s.service.(interface {
		GetIntakeControlRPC(context.Context) (*payoutv1.GetIntakeControlResponse, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "payout intake control unavailable")
	}
	return reader.GetIntakeControlRPC(ctx)
}

func (s *Server) ApplyIntakeControl(ctx context.Context, request *payoutv1.ApplyIntakeControlRequest) (*payoutv1.ApplyIntakeControlResponse, error) {
	reader, ok := s.service.(interface {
		ApplyIntakeControlRPC(context.Context, *payoutv1.ApplyIntakeControlRequest) (*payoutv1.ApplyIntakeControlResponse, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "payout intake control unavailable")
	}
	response, err := reader.ApplyIntakeControlRPC(ctx, request)
	if err != nil {
		if strings.Contains(err.Error(), "revision mismatch") {
			return nil, status.Error(codes.Aborted, "intake revision mismatch")
		}
		return nil, status.Error(codes.InvalidArgument, "invalid intake command")
	}
	return response, nil
}

func parseUserAndAmount(rawUserID, rawAmount string) (uuid.UUID, decimal.Decimal, error) {
	userID, err := uuid.Parse(rawUserID)
	if err != nil {
		return uuid.Nil, decimal.Zero, status.Error(codes.InvalidArgument, "user_id must be a valid UUID")
	}
	amount, err := decimal.NewFromString(rawAmount)
	if err != nil || !amount.IsPositive() || !amount.Equal(amount.Truncate(0)) {
		return uuid.Nil, decimal.Zero, status.Error(codes.InvalidArgument, "amount must be a positive integer decimal string")
	}
	return userID, amount, nil
}

func payoutToProto(value model.PayoutRequest) *payoutv1.Payout {
	return &payoutv1.Payout{Id: value.ID.String(), UserId: value.UserID.String(), Amount: value.Amount.String(), Currency: value.Currency, Vendor: value.Vendor, Status: value.Status, ErrorMessage: value.ErrorMessage, CreatedAt: timestamppb.New(value.CreatedAt), UpdatedAt: timestamppb.New(value.UpdatedAt)}
}
