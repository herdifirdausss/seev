package grpcserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	"github.com/herdifirdausss/seev/internal/payin/model"
)

type Service interface {
	CreateTopupIntent(context.Context, uuid.UUID, decimal.Decimal) (model.TopupIntent, error)
	GetTopupIntent(context.Context, uuid.UUID) (model.TopupIntent, error)
}

type Server struct {
	payinv1.UnimplementedPayinServiceServer
	service                        Service
	notFound                       error
	noRoute                        error
	noVendorAvailable              error
	screeningDependencyUnavailable error
}

func New(service Service, notFound, noRoute, noVendorAvailable, screeningDependencyUnavailable error) *Server {
	return &Server{
		service: service, notFound: notFound, noRoute: noRoute, noVendorAvailable: noVendorAvailable,
		screeningDependencyUnavailable: screeningDependencyUnavailable,
	}
}

func (s *Server) HandleVendorCallback(ctx context.Context, request *payinv1.HandleVendorCallbackRequest) (*payinv1.HandleVendorCallbackResponse, error) {
	handler, ok := s.service.(interface {
		HandleVendorCallback(context.Context, string, string, string, string, string, string, string, string, string, string) (string, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "normalized vendor callback unavailable")
	}
	occurredAt := ""
	if request.GetOccurredAt() != nil {
		occurredAt = request.GetOccurredAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	outcome, err := handler.HandleVendorCallback(ctx, request.GetVendor(), request.GetVendorEventId(), request.GetExternalReference(), request.GetAmount(), request.GetCurrency(), request.GetStatus(), occurredAt, request.GetVendorInboxId(), request.GetRequestId(), request.GetUnknownVendorStatus())
	if err != nil {
		return nil, status.Error(codes.Unavailable, "normalized vendor callback processing failed")
	}
	return &payinv1.HandleVendorCallbackResponse{Result: normalizedCallbackResult(outcome)}, nil
}

func normalizedCallbackResult(outcome string) payinv1.VendorCallbackResult {
	switch outcome {
	case "finalized":
		return payinv1.VendorCallbackResult_VENDOR_CALLBACK_RESULT_FINALIZED
	case "already_finalized":
		return payinv1.VendorCallbackResult_VENDOR_CALLBACK_RESULT_ALREADY_FINALIZED
	case "ignored_non_terminal":
		return payinv1.VendorCallbackResult_VENDOR_CALLBACK_RESULT_IGNORED_NON_TERMINAL
	default:
		return payinv1.VendorCallbackResult_VENDOR_CALLBACK_RESULT_RECORDED_UNMATCHED
	}
}

func (s *Server) CreateTopupIntent(ctx context.Context, request *payinv1.CreateTopupIntentRequest) (*payinv1.CreateTopupIntentResponse, error) {
	userID, amount, err := parseUserAndAmount(request.GetUserId(), request.GetAmount())
	if err != nil {
		return nil, err
	}
	intent, callErr := s.service.CreateTopupIntent(ctx, userID, amount)
	if callErr != nil {
		if strings.Contains(callErr.Error(), "intake paused") {
			return nil, status.Error(codes.FailedPrecondition, "INTAKE_PAUSED")
		}
		if errors.Is(callErr, s.noRoute) {
			return nil, status.Error(codes.FailedPrecondition, "no topup route available")
		}
		if errors.Is(callErr, s.noVendorAvailable) {
			// docs/roadmap/archive/40 Task T2 — distinct gRPC code from "no route"
			// (FailedPrecondition/422): every candidate vendor is
			// registered but circuit-broken, a TRANSIENT condition the
			// caller should retry, not a config problem.
			return nil, status.Error(codes.Unavailable, "no vendor available")
		}
		return nil, status.Error(codes.Internal, "create topup intent failed")
	}
	return &payinv1.CreateTopupIntentResponse{Intent: intentToProto(intent)}, nil
}

func (s *Server) GetTopupIntent(ctx context.Context, request *payinv1.GetTopupIntentRequest) (*payinv1.GetTopupIntentResponse, error) {
	id, err := uuid.Parse(request.GetId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	userID, err := uuid.Parse(request.GetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "user_id must be a valid UUID")
	}
	intent, callErr := s.service.GetTopupIntent(ctx, id)
	if callErr != nil {
		if errors.Is(callErr, s.notFound) {
			return nil, status.Error(codes.NotFound, "topup intent not found")
		}
		return nil, status.Error(codes.Internal, "get topup intent failed")
	}
	if intent.UserID != userID {
		return nil, status.Error(codes.NotFound, "topup intent not found")
	}
	return &payinv1.GetTopupIntentResponse{Intent: intentToProto(intent)}, nil
}

func (s *Server) ListAssuranceRecords(ctx context.Context, request *payinv1.ListAssuranceRecordsRequest) (*payinv1.ListAssuranceRecordsResponse, error) {
	reader, ok := s.service.(interface {
		ListAssuranceRecords(context.Context, *payinv1.ListAssuranceRecordsRequest) (*payinv1.ListAssuranceRecordsResponse, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "payin assurance projection unavailable")
	}
	return reader.ListAssuranceRecords(ctx, request)
}

func (s *Server) GetIntakeControl(ctx context.Context, request *payinv1.GetIntakeControlRequest) (*payinv1.GetIntakeControlResponse, error) {
	reader, ok := s.service.(interface {
		GetIntakeControlRPC(context.Context) (*payinv1.GetIntakeControlResponse, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "payin intake control unavailable")
	}
	return reader.GetIntakeControlRPC(ctx)
}

func (s *Server) ApplyIntakeControl(ctx context.Context, request *payinv1.ApplyIntakeControlRequest) (*payinv1.ApplyIntakeControlResponse, error) {
	reader, ok := s.service.(interface {
		ApplyIntakeControlRPC(context.Context, *payinv1.ApplyIntakeControlRequest) (*payinv1.ApplyIntakeControlResponse, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "payin intake control unavailable")
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
	if err != nil || !amount.Equal(amount.Truncate(0)) || !amount.IsPositive() {
		return uuid.Nil, decimal.Zero, status.Error(codes.InvalidArgument, "amount must be a positive integer decimal string")
	}
	return userID, amount, nil
}

func intentToProto(intent model.TopupIntent) *payinv1.TopupIntent {
	return &payinv1.TopupIntent{
		Id: intent.ID.String(), Reference: intent.Reference, UserId: intent.UserID.String(),
		Amount: intent.Amount.String(), Currency: intent.Currency, Vendor: intent.Vendor, Status: intent.Status,
		ExpiresAt: timestamppb.New(intent.ExpiresAt), CreatedAt: timestamppb.New(intent.CreatedAt), UpdatedAt: timestamppb.New(intent.UpdatedAt),
	}
}
