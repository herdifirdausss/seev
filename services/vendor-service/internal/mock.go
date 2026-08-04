package vendorboundary

import (
	"context"
	"fmt"
	"net/http"

	"github.com/shopspring/decimal"

	vendorgw "github.com/herdifirdausss/seev/contracts/vendorgw"
	vendorv1 "github.com/herdifirdausss/seev/gen/go/vendorservice/v1"
	"github.com/herdifirdausss/seev/services/vendor-service/internal/adapter/mockvendor"
)

// MockAdapter keeps the local development vendor behind the same boundary as
// a real provider. It is composed only by services/vendor-service/cmd/vendor.
type MockAdapter struct {
	name   string
	payout *mockvendor.PayoutProvider
	verify *mockvendor.Verifier
}

func NewMockAdapter(name string, secrets ...string) *MockAdapter {
	secret := ""
	if len(secrets) > 0 {
		secret = secrets[0]
	}
	return &MockAdapter{name: name, payout: mockvendor.NewPayoutProvider(name), verify: mockvendor.New(name, secret)}
}

func (a *MockAdapter) VerifyAndNormalize(headers http.Header, raw []byte) (*NormalizedCallback, error) {
	event, err := a.verify.VerifyAndParse(headers, raw)
	if err != nil || event == nil {
		return nil, err
	}
	return &NormalizedCallback{Flow: "payin", Vendor: a.name, VendorEventID: event.VendorEventID, ExternalReference: event.ExternalRef, Amount: event.Amount.String(), Currency: event.Currency, Status: "settled", OccurredAt: event.OccurredAt}, nil
}

func (a *MockAdapter) SupportsCurrency(operation, currency string) bool {
	return (operation == "topup" || operation == "payout" || operation == "callback") &&
		(currency == "IDR" || currency == "USD")
}

func (a *MockAdapter) CreatePayinSession(_ context.Context, request *vendorv1.CreatePayinSessionRequest) (*vendorv1.CreatePayinSessionResponse, error) {
	if request.GetIntentId() == "" {
		return nil, fmt.Errorf("mockvendor: intent id is required")
	}
	if !a.SupportsCurrency("topup", request.GetCurrency()) {
		return nil, fmt.Errorf("mockvendor: unsupported topup currency %q", request.GetCurrency())
	}
	return &vendorv1.CreatePayinSessionResponse{
		Vendor:          a.name,
		VendorReference: "vref-" + request.GetIntentId(),
		Status:          string(vendorgw.PayoutPending),
		Instructions:    []byte(`{"reference":"` + request.GetIntentId() + `"}`),
	}, nil
}

func (a *MockAdapter) SubmitPayout(ctx context.Context, request *vendorv1.SubmitPayoutRequest) (*vendorv1.PayoutResult, error) {
	if !a.SupportsCurrency("payout", request.GetCurrency()) {
		return nil, fmt.Errorf("mockvendor: unsupported payout currency %q", request.GetCurrency())
	}
	amount, err := decimal.NewFromString(request.GetAmount())
	if err != nil {
		return nil, fmt.Errorf("mockvendor: parse amount: %w", err)
	}
	result, err := a.payout.Submit(ctx, request.GetRequestId(), amount, request.GetCurrency(), request.GetDestination())
	if err != nil {
		return nil, err
	}
	return &vendorv1.PayoutResult{Vendor: a.name, VendorReference: result.VendorRef, Status: string(result.Status), Reason: result.Reason}, nil
}

func (a *MockAdapter) QueryPayout(ctx context.Context, request *vendorv1.QueryPayoutRequest) (*vendorv1.PayoutResult, error) {
	result, err := a.payout.Query(ctx, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	return &vendorv1.PayoutResult{Vendor: a.name, VendorReference: result.VendorRef, Status: string(result.Status), Reason: result.Reason}, nil
}
