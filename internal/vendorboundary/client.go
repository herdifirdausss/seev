package vendorboundary

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/shopspring/decimal"

	vendorv1 "github.com/herdifirdausss/seev/gen/vendorservice/v1"
	"github.com/herdifirdausss/seev/internal/vendorgw"
)

// PayinSessionCreator is the narrow outbound seam Payin uses to ask
// VendorService to create a vendor-side payin session. The intent remains
// owned by Payin; VendorService only owns the vendor call.
type PayinSessionCreator interface {
	CreatePayinSession(context.Context, string, string, decimal.Decimal, string, string) error
}

// Client adapts the versioned VendorService gRPC client to the narrow
// domain-facing seams used by Payin and Payout.
type Client struct {
	rpc vendorv1.VendorServiceClient
}

func NewClient(rpc vendorv1.VendorServiceClient) *Client {
	return &Client{rpc: rpc}
}

// PayinAvailability is a routing marker. It lets Payin select a configured
// vendor without giving Payin access to callback verification.
type PayinAvailability struct{ name string }

func NewPayinAvailability(name string) vendorgw.PayinVendor { return PayinAvailability{name: name} }

func (a PayinAvailability) Vendor() string { return a.name }

func (c *Client) CreatePayinSession(ctx context.Context, vendor, intentID string, amount decimal.Decimal, currency, requestID string) error {
	if c == nil || c.rpc == nil {
		return fmt.Errorf("vendorboundary: nil VendorService client")
	}
	response, err := c.rpc.CreatePayinSession(ctx, &vendorv1.CreatePayinSessionRequest{
		Vendor: vendor, IntentId: intentID, Amount: amount.String(), Currency: currency, RequestId: requestID,
	})
	if err != nil {
		return err
	}
	if response.GetVendorReference() == "" {
		return fmt.Errorf("vendorboundary: VendorService returned empty vendor reference")
	}
	return nil
}

// PayoutProvider translates the VendorService payout contract into the
// legacy provider interface used by payout's durable relay. It contains no
// vendor implementation and therefore cannot bypass VendorService.
type PayoutProvider struct {
	name      string
	rpc       vendorv1.VendorServiceClient
	forceFail atomic.Bool
}

func NewPayoutProvider(name string, rpc vendorv1.VendorServiceClient) *PayoutProvider {
	return &PayoutProvider{name: name, rpc: rpc}
}

func (p *PayoutProvider) Vendor() string { return p.name }

// SetForceFail is test-only chaos control retained at the payout boundary. It
// fails before the RPC, so the relay can exercise breaker/failover behavior
// while normal vendor traffic remains delegated to VendorService.
func (p *PayoutProvider) SetForceFail(fail bool) {
	if p != nil {
		p.forceFail.Store(fail)
	}
}

func (p *PayoutProvider) Submit(ctx context.Context, idempotencyKey string, amount decimal.Decimal, currency string, destination json.RawMessage) (vendorgw.PayoutResult, error) {
	if p == nil || p.rpc == nil {
		return vendorgw.PayoutResult{}, fmt.Errorf("vendorboundary: nil VendorService client")
	}
	if p.forceFail.Load() {
		return vendorgw.PayoutResult{}, fmt.Errorf("vendorboundary: forced failure for %s", p.name)
	}
	response, err := p.rpc.SubmitPayout(ctx, &vendorv1.SubmitPayoutRequest{
		Vendor: p.name, RequestId: idempotencyKey, Amount: amount.String(), Currency: currency, Destination: destination,
	})
	if err != nil {
		return vendorgw.PayoutResult{}, err
	}
	return payoutResult(response.GetResult())
}

func (p *PayoutProvider) Query(ctx context.Context, idempotencyKey string) (vendorgw.PayoutResult, error) {
	if p == nil || p.rpc == nil {
		return vendorgw.PayoutResult{}, fmt.Errorf("vendorboundary: nil VendorService client")
	}
	response, err := p.rpc.QueryPayout(ctx, &vendorv1.QueryPayoutRequest{Vendor: p.name, RequestId: idempotencyKey})
	if err != nil {
		return vendorgw.PayoutResult{}, err
	}
	return payoutResult(response.GetResult())
}

func payoutResult(result *vendorv1.PayoutResult) (vendorgw.PayoutResult, error) {
	if result == nil || result.GetVendorReference() == "" {
		return vendorgw.PayoutResult{}, fmt.Errorf("vendorboundary: VendorService returned empty payout result")
	}
	status := vendorgw.PayoutStatus(result.GetStatus())
	switch status {
	case vendorgw.PayoutSettled, vendorgw.PayoutPending, vendorgw.PayoutFailed:
	default:
		return vendorgw.PayoutResult{}, fmt.Errorf("vendorboundary: unknown payout status %q", status)
	}
	return vendorgw.PayoutResult{VendorRef: result.GetVendorReference(), Status: status, Reason: result.GetReason()}, nil
}
