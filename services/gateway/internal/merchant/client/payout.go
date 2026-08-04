package client

import (
	"context"
	"time"

	"github.com/google/uuid"

	payoutv1 "github.com/herdifirdausss/seev/gen/go/payout/v1"
)

// PayoutResult is the Gateway-side projection of a merchant payout —
// deliberately its own type, not payoutv1.Payout, so services/gateway/internal/merchant/api
// never imports a generated proto package for its own DTO mapping. Status
// carries PayoutService's own internal values (submitted | vendor_pending |
// settled | cancelled | ...) UNTRANSLATED — mapping those onto the locked
// public enum (docs/reference/c1-b2b-design.md §1.3) is services/gateway/internal/merchant/api's
// job, not this package's. Destination is deliberately NOT included here,
// mirroring the existing user-facing payout response's own omission of it
// (services/gateway/internal/transport/http/payout.go's payoutResponse) — the caller already has
// it, and this Result must not become a place a destination round-trips
// back out unnecessarily.
type PayoutResult struct {
	ID           uuid.UUID
	Status       string
	Currency     string
	Amount       string
	Vendor       string
	ErrorMessage string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// PayoutClient is the typed Gateway-side client to PayoutService's
// merchant-only RPCs (Plan 57, C1).
type PayoutClient struct {
	grpc payoutv1.PayoutServiceClient
}

func NewPayoutClient(grpcClient payoutv1.PayoutServiceClient) *PayoutClient {
	if grpcClient == nil {
		panic("merchant/client: NewPayoutClient requires a non-nil PayoutServiceClient")
	}
	return &PayoutClient{grpc: grpcClient}
}

// CreatePayout calls PayoutService's CreateMerchantPayout. tenantID/
// environment must come from the authenticated principal, never a
// caller-suppliable request field (§3.2). downstreamKey is Gateway's own
// idempotency.DownstreamKey — a retry with the same key recovers the
// ORIGINAL request rather than placing a second hold (§10.4).
func (c *PayoutClient) CreatePayout(ctx context.Context, tenantID uuid.UUID, environment, currency, amount string, destination []byte, createdBy, downstreamKey string) (PayoutResult, error) {
	resp, err := c.grpc.CreateMerchantPayout(ctx, &payoutv1.CreateMerchantPayoutRequest{
		TenantId: tenantID.String(), Environment: environment, Currency: currency, Amount: amount,
		Destination: destination, CreatedBy: createdBy, DownstreamKey: downstreamKey,
	})
	if err != nil {
		return PayoutResult{}, translateError(err)
	}
	return payoutResultFromProto(resp.GetPayout()), nil
}

// GetPayout calls PayoutService's GetMerchantPayout, tenant-scoped (§7.3) —
// a payout owned by a different tenant returns the SAME ErrNotFound as one
// that doesn't exist at all (§6.7).
func (c *PayoutClient) GetPayout(ctx context.Context, tenantID, id uuid.UUID) (PayoutResult, error) {
	resp, err := c.grpc.GetMerchantPayout(ctx, &payoutv1.GetMerchantPayoutRequest{TenantId: tenantID.String(), Id: id.String()})
	if err != nil {
		return PayoutResult{}, translateError(err)
	}
	return payoutResultFromProto(resp.GetPayout()), nil
}

func payoutResultFromProto(payout *payoutv1.Payout) PayoutResult {
	id, _ := uuid.Parse(payout.GetId())
	return PayoutResult{
		ID: id, Status: payout.GetStatus(), Currency: payout.GetCurrency(), Amount: payout.GetAmount(),
		Vendor: payout.GetVendor(), ErrorMessage: payout.GetErrorMessage(),
		CreatedAt: payout.GetCreatedAt().AsTime(), UpdatedAt: payout.GetUpdatedAt().AsTime(),
	}
}
