package grpcserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	"github.com/herdifirdausss/seev/internal/payin/model"
	currencyreg "github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
)

type Service interface {
	CreateTopupIntent(context.Context, uuid.UUID, decimal.Decimal) (model.TopupIntent, error)
	GetTopupIntent(context.Context, uuid.UUID) (model.TopupIntent, error)
}

var errCurrencyAwareTopupUnavailable = errors.New("currency-aware topup creation unavailable")

type Server struct {
	payinv1.UnimplementedPayinServiceServer
	service                        Service
	notFound                       error
	noRoute                        error
	noVendorAvailable              error
	screeningDependencyUnavailable error
	sandboxVendorUnavailable       error
}

func New(service Service, notFound, noRoute, noVendorAvailable, screeningDependencyUnavailable, sandboxVendorUnavailable error) *Server {
	return &Server{
		service: service, notFound: notFound, noRoute: noRoute, noVendorAvailable: noVendorAvailable,
		screeningDependencyUnavailable: screeningDependencyUnavailable,
		sandboxVendorUnavailable:       sandboxVendorUnavailable,
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
	var intent model.TopupIntent
	var callErr error
	rawQuoteID := protoStringField(request, "fee_quote_id")
	if rawQuoteID == "" {
		if values := metadata.ValueFromIncomingContext(ctx, "seev-fee-quote-id"); len(values) > 0 {
			rawQuoteID = values[0]
		}
	}
	if rawQuoteID != "" {
		quoteID, parseErr := uuid.Parse(rawQuoteID)
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "fee_quote_id must be a valid UUID")
		}
		requestedCurrency := ""
		if values := metadata.ValueFromIncomingContext(ctx, "x-seev-currency"); len(values) > 0 {
			requestedCurrency = values[0]
		}
		if requestedCurrency != "" {
			creator, ok := s.service.(interface {
				CreateTopupIntentWithCurrencyAndFeeQuote(context.Context, uuid.UUID, decimal.Decimal, string, uuid.UUID) (model.TopupIntent, error)
			})
			if !ok {
				return nil, status.Error(codes.Unimplemented, "currency-aware topup fee quote consumption unavailable")
			}
			intent, callErr = creator.CreateTopupIntentWithCurrencyAndFeeQuote(ctx, userID, amount, requestedCurrency, quoteID)
		} else {
			creator, ok := s.service.(interface {
				CreateTopupIntentWithFeeQuote(context.Context, uuid.UUID, decimal.Decimal, uuid.UUID) (model.TopupIntent, error)
			})
			if !ok {
				return nil, status.Error(codes.Unimplemented, "topup fee quote consumption unavailable")
			}
			intent, callErr = creator.CreateTopupIntentWithFeeQuote(ctx, userID, amount, quoteID)
		}
	} else {
		intent, callErr = s.createTopupIntent(ctx, userID, amount)
	}
	if callErr != nil {
		if errors.Is(callErr, errCurrencyAwareTopupUnavailable) {
			return nil, status.Error(codes.Unimplemented, errCurrencyAwareTopupUnavailable.Error())
		}
		if errors.Is(callErr, currencyreg.ErrInvalidCurrency) {
			return nil, status.Error(codes.InvalidArgument, "CURRENCY_INVALID: currency must be exactly three uppercase letters")
		}
		var business *ledgererr.LedgerError
		if errors.As(callErr, &business) {
			if business.Code == "CURRENCY_INVALID" {
				return nil, status.Error(codes.InvalidArgument, business.Error())
			}
			return nil, status.Error(codes.FailedPrecondition, business.Error())
		}
		if status.Code(callErr) == codes.InvalidArgument {
			return nil, status.Error(codes.InvalidArgument, status.Convert(callErr).Message())
		}
		if strings.Contains(callErr.Error(), "intake paused") {
			return nil, status.Error(codes.FailedPrecondition, "INTAKE_PAUSED")
		}
		if strings.Contains(callErr.Error(), "topup fee quote required") {
			return nil, status.Error(codes.FailedPrecondition, "TOPUP_FEE_QUOTE_REQUIRED")
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
	if err := setFeeSnapshotHeaders(ctx, intent); err != nil {
		return nil, status.Error(codes.Internal, "set topup fee snapshot metadata failed")
	}
	return &payinv1.CreateTopupIntentResponse{Intent: intentToProto(intent)}, nil
}

func (s *Server) createTopupIntent(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (model.TopupIntent, error) {
	if incoming, ok := metadata.FromIncomingContext(ctx); ok {
		if values := incoming.Get("x-seev-currency"); len(values) > 0 && values[0] != "" {
			if creator, ok := s.service.(interface {
				CreateTopupIntentWithCurrency(context.Context, uuid.UUID, decimal.Decimal, string) (model.TopupIntent, error)
			}); ok {
				return creator.CreateTopupIntentWithCurrency(ctx, userID, amount, values[0])
			}
			return model.TopupIntent{}, errCurrencyAwareTopupUnavailable
		}
	}
	return s.service.CreateTopupIntent(ctx, userID, amount)
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
	if err := setFeeSnapshotHeaders(ctx, intent); err != nil {
		return nil, status.Error(codes.Internal, "set topup fee snapshot metadata failed")
	}
	return &payinv1.GetTopupIntentResponse{Intent: intentToProto(intent)}, nil
}

// CreateMerchantTopupIntent is Gateway-only (Plan 57, C1) — the merchant
// counterpart of CreateTopupIntent, reached via the optional-interface
// type assertion below rather than a Service interface change so this
// package's core Service contract stays untouched (mirrors
// ListAssuranceRecords/GetIntakeControl's own pattern in this file).
func (s *Server) CreateMerchantTopupIntent(ctx context.Context, request *payinv1.CreateMerchantTopupIntentRequest) (*payinv1.CreateMerchantTopupIntentResponse, error) {
	creator, ok := s.service.(interface {
		CreateMerchantTopupIntent(context.Context, uuid.UUID, string, string, decimal.Decimal, string) (model.TopupIntent, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "merchant topup intent creation unavailable")
	}
	tenantID, err := uuid.Parse(request.GetTenantId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "tenant_id must be a valid UUID")
	}
	amount, err := decimal.NewFromString(request.GetAmount())
	if err != nil || currencyreg.ValidatePositiveMinorAmount(amount) != nil {
		return nil, status.Error(codes.InvalidArgument, "amount must be a positive integer decimal string")
	}
	if request.GetDownstreamKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "downstream_key is required")
	}
	intent, callErr := creator.CreateMerchantTopupIntent(ctx, tenantID, request.GetEnvironment(), request.GetCurrency(), amount, request.GetDownstreamKey())
	if callErr != nil {
		if errors.Is(callErr, currencyreg.ErrInvalidCurrency) {
			return nil, status.Error(codes.InvalidArgument, "CURRENCY_INVALID: currency must be exactly three uppercase letters")
		}
		if strings.Contains(callErr.Error(), "intake paused") {
			return nil, status.Error(codes.FailedPrecondition, "INTAKE_PAUSED")
		}
		if strings.Contains(callErr.Error(), "topup fee quote required") {
			return nil, status.Error(codes.FailedPrecondition, "TOPUP_FEE_QUOTE_REQUIRED")
		}
		if errors.Is(callErr, s.noRoute) {
			return nil, status.Error(codes.FailedPrecondition, "no topup route available")
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
			if business.Code == "CURRENCY_INVALID" {
				return nil, status.Error(codes.InvalidArgument, business.Error())
			}
			return nil, status.Error(codes.FailedPrecondition, business.Error())
		}
		return nil, status.Error(codes.Internal, "create merchant topup intent failed")
	}
	return &payinv1.CreateMerchantTopupIntentResponse{Intent: intentToProto(intent)}, nil
}

// GetMerchantTopupIntent is Gateway-only (Plan 57, C1) — the tenant-scoped
// counterpart of GetTopupIntent (§7.3: every merchant-owned read must be
// scoped by tenant_id, enforced by the service's own GetMerchantTopupIntent,
// not re-derived here).
func (s *Server) GetMerchantTopupIntent(ctx context.Context, request *payinv1.GetMerchantTopupIntentRequest) (*payinv1.GetMerchantTopupIntentResponse, error) {
	reader, ok := s.service.(interface {
		GetMerchantTopupIntent(context.Context, uuid.UUID, uuid.UUID) (model.TopupIntent, error)
	})
	if !ok {
		return nil, status.Error(codes.Unimplemented, "merchant topup intent read unavailable")
	}
	tenantID, err := uuid.Parse(request.GetTenantId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "tenant_id must be a valid UUID")
	}
	id, err := uuid.Parse(request.GetId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	intent, callErr := reader.GetMerchantTopupIntent(ctx, tenantID, id)
	if callErr != nil {
		if errors.Is(callErr, s.notFound) {
			return nil, status.Error(codes.NotFound, "topup intent not found")
		}
		return nil, status.Error(codes.Internal, "get merchant topup intent failed")
	}
	return &payinv1.GetMerchantTopupIntentResponse{Intent: intentToProto(intent)}, nil
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
	if err != nil || currencyreg.ValidatePositiveMinorAmount(amount) != nil {
		return uuid.Nil, decimal.Zero, status.Error(codes.InvalidArgument, "amount must be a positive integer decimal string")
	}
	return userID, amount, nil
}

func intentToProto(intent model.TopupIntent) *payinv1.TopupIntent {
	intent.NormalizeFinancials()
	out := &payinv1.TopupIntent{
		Id: intent.ID.String(), Reference: intent.Reference, UserId: intent.UserID.String(),
		Amount: intent.Amount.String(), Currency: intent.Currency, Vendor: intent.Vendor, Status: intent.Status,
		ExpiresAt: timestamppb.New(intent.ExpiresAt), CreatedAt: timestamppb.New(intent.CreatedAt), UpdatedAt: timestamppb.New(intent.UpdatedAt),
	}
	setProtoStringField(out, "fee_amount", intent.FeeAmount.String())
	setProtoStringField(out, "total_debit", intent.TotalDebit.String())
	setProtoStringField(out, "fee_gateway", intent.FeeGateway)
	if intent.FeeQuoteID != nil {
		setProtoStringField(out, "fee_quote_id", intent.FeeQuoteID.String())
	}
	setProtoStringField(out, "fee_application", intent.FeeApplication)
	return out
}

func setFeeSnapshotHeaders(ctx context.Context, intent model.TopupIntent) error {
	values := metadata.Pairs(
		"seev-fee-amount", intent.FeeAmount.String(),
		"seev-total-debit", intent.TotalDebit.String(),
		"seev-fee-application", intent.FeeApplication,
	)
	if intent.FeeGateway != "" {
		values.Append("seev-fee-gateway", intent.FeeGateway)
	}
	if intent.FeeQuoteID != nil {
		values.Append("seev-fee-quote-id", intent.FeeQuoteID.String())
	}
	return grpc.SetHeader(ctx, values)
}

// Reflection keeps this source compatible with the checked-in generated
// protobuf during the expand/contract window; once the proto additions are
// regenerated, the same helper reads/writes the new fields over the wire.
func protoStringField(message interface{ ProtoReflect() protoreflect.Message }, name string) string {
	field := message.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || field.Kind() != protoreflect.StringKind {
		return ""
	}
	return message.ProtoReflect().Get(field).String()
}

func setProtoStringField(message interface{ ProtoReflect() protoreflect.Message }, name, value string) {
	field := message.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || field.Kind() != protoreflect.StringKind {
		return
	}
	message.ProtoReflect().Set(field, protoreflect.ValueOfString(value))
}
