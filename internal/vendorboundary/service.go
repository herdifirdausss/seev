// Package vendorboundary contains the transport-neutral VendorService boundary.
// Vendor-specific wire clients are composed here, never in Gateway, Payin,
// or Payout.
package vendorboundary

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/shopspring/decimal"
	vendorv1 "github.com/herdifirdausss/seev/gen/vendorservice/v1"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	currencyreg "github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalerror"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ErrUnknownVendor = errors.New("vendor: unknown vendor")

type Adapter interface {
	CreatePayinSession(context.Context, *vendorv1.CreatePayinSessionRequest) (*vendorv1.CreatePayinSessionResponse, error)
	SubmitPayout(context.Context, *vendorv1.SubmitPayoutRequest) (*vendorv1.PayoutResult, error)
	QueryPayout(context.Context, *vendorv1.QueryPayoutRequest) (*vendorv1.PayoutResult, error)
}

type Registry struct{ adapters map[string]Adapter }

func NewRegistry() *Registry { return &Registry{adapters: make(map[string]Adapter)} }

func (r *Registry) Add(name string, adapter Adapter) error {
	if name == "" || adapter == nil {
		return errors.New("vendor: adapter name and implementation are required")
	}
	if _, exists := r.adapters[name]; exists {
		return errors.New("vendor: duplicate adapter")
	}
	r.adapters[name] = adapter
	return nil
}

type Server struct {
	vendorv1.UnimplementedVendorServiceServer
	registry *Registry
	db       *database.DBSQL
}

func NewServer(registry *Registry, db ...*database.DBSQL) *Server {
	server := &Server{registry: registry}
	if len(db) > 0 {
		server.db = db[0]
	}
	return server
}

func (s *Server) adapter(name string) (Adapter, error) {
	if s.registry == nil {
		return nil, ErrUnknownVendor
	}
	adapter, ok := s.registry.adapters[name]
	if !ok {
		return nil, ErrUnknownVendor
	}
	return adapter, nil
}

func (r *Registry) callbackAdapter(name string) (CallbackVerifier, error) {
	if r == nil {
		return nil, ErrUnknownVendor
	}
	adapter, ok := r.adapters[name]
	if !ok {
		return nil, ErrUnknownVendor
	}
	verifier, ok := adapter.(CallbackVerifier)
	if !ok {
		return nil, fmt.Errorf("vendor: callback verifier unavailable")
	}
	return verifier, nil
}

func (s *Server) CreatePayinSession(ctx context.Context, request *vendorv1.CreatePayinSessionRequest) (*vendorv1.CreatePayinSessionResponse, error) {
	if request.GetVendor() == "" || request.GetIntentId() == "" || request.GetRequestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "vendor, intent_id, and request_id are required")
	}
	if err := validateVendorAmount(request.GetAmount(), request.GetCurrency()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	adapter, err := s.adapter(request.GetVendor())
	if err != nil {
		return nil, status.Error(codes.NotFound, "unknown vendor")
	}
	if !vendorgw.SupportsRequestedCurrency(adapter, "topup", request.GetCurrency()) {
		return nil, status.Error(codes.FailedPrecondition, "vendor does not support requested currency")
	}
	result, err := adapter.CreatePayinSession(ctx, request)
	if err != nil || result == nil {
		vendorOutboundAttempts.WithLabelValues("payin", request.GetVendor(), "create_payin_session", "error").Inc()
		serverRecordOutbound(ctx, s.db, "payin", request.GetVendor(), request.GetRequestId(), "", "create_payin_session", "error")
		return nil, status.Error(codes.Unavailable, "vendor session unavailable")
	}
	vendorOutboundAttempts.WithLabelValues("payin", request.GetVendor(), "create_payin_session", "accepted").Inc()
	serverRecordOutbound(ctx, s.db, "payin", request.GetVendor(), request.GetRequestId(), result.GetVendorReference(), "create_payin_session", "accepted")
	return result, nil
}

func (s *Server) SubmitPayout(ctx context.Context, request *vendorv1.SubmitPayoutRequest) (*vendorv1.SubmitPayoutResponse, error) {
	if request.GetVendor() == "" || request.GetRequestId() == "" || request.GetAmount() == "" || request.GetCurrency() == "" || len(request.GetDestination()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "vendor, request_id, amount, currency, and destination are required")
	}
	if err := validateVendorAmount(request.GetAmount(), request.GetCurrency()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	adapter, err := s.adapter(request.GetVendor())
	if err != nil {
		return nil, status.Error(codes.NotFound, "unknown vendor")
	}
	if !vendorgw.SupportsRequestedCurrency(adapter, "payout", request.GetCurrency()) {
		return nil, status.Error(codes.FailedPrecondition, "vendor does not support requested currency")
	}
	result, err := adapter.SubmitPayout(ctx, request)
	if err != nil || result == nil {
		vendorOutboundAttempts.WithLabelValues("payout", request.GetVendor(), "submit_payout", "error").Inc()
		serverRecordOutbound(ctx, s.db, "payout", request.GetVendor(), request.GetRequestId(), "", "submit_payout", "error")
		return nil, status.Error(codes.Unavailable, "vendor submission unavailable")
	}
	vendorOutboundAttempts.WithLabelValues("payout", request.GetVendor(), "submit_payout", "accepted").Inc()
	serverRecordOutbound(ctx, s.db, "payout", request.GetVendor(), request.GetRequestId(), result.GetVendorReference(), "submit_payout", "accepted")
	return &vendorv1.SubmitPayoutResponse{Result: result}, nil
}

func validateVendorAmount(rawAmount, code string) error {
	if err := currencyreg.ValidateCode(code); err != nil {
		return fmt.Errorf("currency must be three uppercase ASCII letters")
	}
	amount, err := decimal.NewFromString(rawAmount)
	if err != nil || currencyreg.ValidatePositiveMinorAmount(amount) != nil {
		return fmt.Errorf("amount must be a positive integer minor-unit string")
	}
	return nil
}

func (s *Server) QueryPayout(ctx context.Context, request *vendorv1.QueryPayoutRequest) (*vendorv1.QueryPayoutResponse, error) {
	if request.GetVendor() == "" || request.GetRequestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "vendor and request_id are required")
	}
	adapter, err := s.adapter(request.GetVendor())
	if err != nil {
		return nil, status.Error(codes.NotFound, "unknown vendor")
	}
	result, err := adapter.QueryPayout(ctx, request)
	if err != nil || result == nil {
		vendorOutboundAttempts.WithLabelValues("payout", request.GetVendor(), "query_payout", "error").Inc()
		serverRecordOutbound(ctx, s.db, "payout", request.GetVendor(), request.GetRequestId(), "", "query_payout", "error")
		return nil, status.Error(codes.Unavailable, "vendor query unavailable")
	}
	vendorOutboundAttempts.WithLabelValues("payout", request.GetVendor(), "query_payout", "accepted").Inc()
	serverRecordOutbound(ctx, s.db, "payout", request.GetVendor(), request.GetRequestId(), result.GetVendorReference(), "query_payout", "accepted")
	return &vendorv1.QueryPayoutResponse{Result: result}, nil
}

// maxRecordOutboundAttempts bounds serverRecordOutbound's retry-on-conflict
// loop. The next `attempt` number is computed as MAX(attempt)+1 in the same
// statement that inserts it, so two concurrent calls for the same (flow,
// request_id, operation) — most commonly overlapping QueryPayout status
// polls — can compute the same next value and collide on
// vendor_outbound_attempts's UNIQUE(flow, request_id, attempt, operation)
// constraint. Discovered live: the losing insert used to be discarded via
// `_, _ = db.ExecContext(...)`, silently dropping a row from what
// config/data-retention.yaml declares a permanent audit trail. A retry
// re-reads MAX(attempt) fresh, which now includes the row the other call
// just committed, so it picks a new, non-colliding value.
const maxRecordOutboundAttempts = 3

func serverRecordOutbound(ctx context.Context, db *database.DBSQL, flow, vendor, requestID, vendorReference, operation, outcome string) {
	if db == nil {
		return
	}
	var err error
	for range maxRecordOutboundAttempts {
		_, err = db.ExecContext(ctx, `INSERT INTO vendor_outbound_attempts
			(flow, vendor, request_id, vendor_reference, attempt, operation, outcome, sanitized_response)
			SELECT $1,$2,$3,$4,COALESCE(MAX(attempt),0)+1,$5,$6,$7::jsonb
			FROM vendor_outbound_attempts WHERE flow=$1 AND request_id=$3 AND operation=$5`,
			flow, vendor, requestID, vendorReference, operation, outcome, `{"outcome":"`+outcome+`"}`)
		if err == nil {
			return
		}
		if !generalerror.IsDuplicateKey(err) {
			break
		}
	}
	slog.Default().Error("vendorboundary: record outbound attempt failed",
		slog.String("flow", flow), slog.String("vendor", vendor), slog.String("operation", operation), slog.Any("error", err))
}
