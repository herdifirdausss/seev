package grpcserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/internal/vendorgw"
)

type Service interface {
	HandleWebhookResult(context.Context, string, http.Header, []byte) (string, error)
	// The vendor argument is transitional until routing becomes DB-driven in T2.
	CreateTopupIntent(context.Context, uuid.UUID, decimal.Decimal) (model.TopupIntent, error)
	GetTopupIntent(context.Context, uuid.UUID) (model.TopupIntent, error)
}

type Server struct {
	payinv1.UnimplementedPayinServiceServer
	service  Service
	notFound error
	noRoute  error
}

func New(service Service, notFound, noRoute error) *Server {
	return &Server{service: service, notFound: notFound, noRoute: noRoute}
}

func (s *Server) HandleWebhook(ctx context.Context, request *payinv1.HandleWebhookRequest) (*payinv1.HandleWebhookResponse, error) {
	headers := make(http.Header, len(request.GetHeaders()))
	for key, value := range request.GetHeaders() {
		headers.Set(key, value)
	}
	outcome, err := s.service.HandleWebhookResult(ctx, request.GetVendor(), headers, request.GetRawBody())
	if outcome == "business_failure" {
		return &payinv1.HandleWebhookResponse{Result: payinv1.WebhookResult_WEBHOOK_RESULT_BUSINESS_FAILURE}, nil
	}
	if err != nil {
		switch {
		case errors.Is(err, vendorgw.ErrUnknownPayinVendor):
			return nil, status.Error(codes.NotFound, "unknown vendor")
		case errors.Is(err, vendorgw.ErrInvalidSignature):
			return nil, status.Error(codes.Unauthenticated, "invalid webhook signature")
		default:
			return nil, status.Error(codes.Internal, "payin webhook processing failed")
		}
	}
	result := payinv1.WebhookResult_WEBHOOK_RESULT_OK
	if outcome == "ignored" {
		result = payinv1.WebhookResult_WEBHOOK_RESULT_IGNORED
	}
	return &payinv1.HandleWebhookResponse{Result: result}, nil
}

func (s *Server) CreateTopupIntent(ctx context.Context, request *payinv1.CreateTopupIntentRequest) (*payinv1.CreateTopupIntentResponse, error) {
	userID, amount, err := parseUserAndAmount(request.GetUserId(), request.GetAmount())
	if err != nil {
		return nil, err
	}
	intent, callErr := s.service.CreateTopupIntent(ctx, userID, amount)
	if callErr != nil {
		if errors.Is(callErr, s.noRoute) {
			return nil, status.Error(codes.FailedPrecondition, "no topup route available")
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
