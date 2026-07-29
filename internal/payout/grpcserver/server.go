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
	sandboxVendorUnavailable       error
}

func New(service Service, notFound, noRoute, noVendorAvailable, screeningBlocked, screeningDependencyUnavailable, sandboxVendorUnavailable error) *Server {
	return &Server{
		service: service, notFound: notFound, noRoute: noRoute, noVendorAvailable: noVendorAvailable,
		screeningBlocked: screeningBlocked, screeningDependencyUnavailable: screeningDependencyUnavailable,
		sandboxVendorUnavailable: sandboxVendorUnavailable,
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
			// docs/roadmap/archive/40 Task T2 — distinct gRPC code from "no route"
			// (FailedPrecondition/422): every candidate vendor is
			// registered but circuit-broken, a TRANSIENT condition the
			// caller should retry, not a config problem.
			return nil, status.Error(codes.Unavailable, "no vendor available")
		}
		if errors.Is(callErr, s.screeningBlocked) {
			return nil, status.Error(codes.FailedPrecondition, callErr.Error())
		}
		if errors.Is(callErr, s.screeningDependencyUnavailable) {
			// docs/roadmap/archive/45 Task T3/K4 — codes.Unavailable like
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

// CreateMerchantPayout is Gateway-only (Plan 57, C1) — the merchant
// counterpart of CreatePayout, reached via the optional-interface type
// assertion below rather than a Service interface change so this
// package's core Service contract stays untouched (mirrors
// ListAssuranceRecords/GetIntakeControl's own pattern in this file). No
// screeningBlocked/screeningDependencyUnavailable branch: fraud screening
// is deliberately skipped for merchant-owned payouts (Plan 57 T6 scope
// decision), so CreateMerchant never returns those errors.
func (s *Server) CreateMerchantPayout(ctx context.Context, request *payoutv1.CreateMerchantPayoutRequest) (*payoutv1.CreateMerchantPayoutResponse, error) {
	creator, ok := s.service.(interface {
		CreateMerchant(context.Context, uuid.UUID, string, string, decimal.Decimal, []byte, string, string) (uuid.UUID, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "merchant payout creation unavailable")
	}
	tenantID, err := uuid.Parse(request.GetTenantId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "tenant_id must be a valid UUID")
	}
	amount, err := decimal.NewFromString(request.GetAmount())
	if err != nil || !amount.IsPositive() || !amount.Equal(amount.Truncate(0)) {
		return nil, status.Error(codes.InvalidArgument, "amount must be a positive integer decimal string")
	}
	if len(request.GetDestination()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "destination is required")
	}
	if request.GetDownstreamKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "downstream_key is required")
	}
	id, callErr := creator.CreateMerchant(ctx, tenantID, request.GetEnvironment(), request.GetCurrency(), amount, request.GetDestination(), request.GetCreatedBy(), request.GetDownstreamKey())
	if callErr != nil {
		if strings.Contains(callErr.Error(), "intake paused") {
			return nil, status.Error(codes.FailedPrecondition, "INTAKE_PAUSED")
		}
		if errors.Is(callErr, s.noRoute) {
			return nil, status.Error(codes.FailedPrecondition, "no payout route available")
		}
		if errors.Is(callErr, s.noVendorAvailable) {
			return nil, status.Error(codes.Unavailable, "no vendor available")
		}
		if errors.Is(callErr, s.sandboxVendorUnavailable) {
			// A sandbox tenant must fail closed if the mock vendor isn't
			// registered — distinct from a live "no route" config problem.
			return nil, status.Error(codes.FailedPrecondition, "SANDBOX_VENDOR_UNAVAILABLE")
		}
		var business *ledgererr.LedgerError
		if errors.As(callErr, &business) {
			return nil, status.Error(codes.FailedPrecondition, business.Error())
		}
		return nil, status.Error(codes.Internal, "create merchant payout failed")
	}
	value, getErr := s.service.Get(ctx, id)
	if getErr != nil {
		return nil, status.Error(codes.Internal, "read created merchant payout failed")
	}
	return &payoutv1.CreateMerchantPayoutResponse{Payout: payoutToProto(value)}, nil
}

// GetMerchantPayout is Gateway-only (Plan 57, C1) — the tenant-scoped
// counterpart of GetPayout (§7.3: every merchant-owned read must be scoped
// by tenant_id, enforced by the service's own GetMerchant, not re-derived
// here).
func (s *Server) GetMerchantPayout(ctx context.Context, request *payoutv1.GetMerchantPayoutRequest) (*payoutv1.GetMerchantPayoutResponse, error) {
	reader, ok := s.service.(interface {
		GetMerchant(context.Context, uuid.UUID, uuid.UUID) (model.PayoutRequest, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "merchant payout read unavailable")
	}
	tenantID, err := uuid.Parse(request.GetTenantId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "tenant_id must be a valid UUID")
	}
	id, err := uuid.Parse(request.GetId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	value, callErr := reader.GetMerchant(ctx, tenantID, id)
	if callErr != nil {
		if errors.Is(callErr, s.notFound) {
			return nil, status.Error(codes.NotFound, "payout request not found")
		}
		return nil, status.Error(codes.Internal, "get merchant payout failed")
	}
	return &payoutv1.GetMerchantPayoutResponse{Payout: payoutToProto(value)}, nil
}

func (s *Server) HandleVendorCallback(ctx context.Context, request *payoutv1.HandleVendorCallbackRequest) (*payoutv1.HandleVendorCallbackResponse, error) {
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
	return &payoutv1.HandleVendorCallbackResponse{Result: normalizedCallbackResult(outcome)}, nil
}

func normalizedCallbackResult(outcome string) payoutv1.VendorCallbackResult {
	switch outcome {
	case "finalized":
		return payoutv1.VendorCallbackResult_VENDOR_CALLBACK_RESULT_FINALIZED
	case "already_finalized":
		return payoutv1.VendorCallbackResult_VENDOR_CALLBACK_RESULT_ALREADY_FINALIZED
	case "ignored_non_terminal":
		return payoutv1.VendorCallbackResult_VENDOR_CALLBACK_RESULT_IGNORED_NON_TERMINAL
	default:
		return payoutv1.VendorCallbackResult_VENDOR_CALLBACK_RESULT_RECORDED_UNMATCHED
	}
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
