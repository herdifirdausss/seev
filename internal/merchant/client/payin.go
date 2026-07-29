package client

import (
	"context"
	"time"

	"github.com/google/uuid"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
)

// PayinResult is the Gateway-side projection of a merchant pay-in intent —
// deliberately its own type, not payinv1.TopupIntent, so internal/merchant/api
// never imports a generated proto package for its own DTO mapping. Status
// carries PayinService's own internal values (pending | settled | expired)
// UNTRANSLATED — mapping those onto the locked public enum
// (docs/reference/c1-b2b-design.md §1.2) is internal/merchant/api's job,
// not this package's.
type PayinResult struct {
	ID        uuid.UUID
	Status    string
	Currency  string
	Amount    string
	Vendor    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PayinClient is the typed Gateway-side client to PayinService's
// merchant-only RPCs (Plan 57, C1).
type PayinClient struct {
	grpc payinv1.PayinServiceClient
}

func NewPayinClient(grpcClient payinv1.PayinServiceClient) *PayinClient {
	if grpcClient == nil {
		panic("merchant/client: NewPayinClient requires a non-nil PayinServiceClient")
	}
	return &PayinClient{grpc: grpcClient}
}

// CreateTopupIntent calls PayinService's CreateMerchantTopupIntent.
// tenantID/environment must come from the authenticated principal, never
// a caller-suppliable request field (§3.2). downstreamKey is Gateway's own
// idempotency.DownstreamKey — a retry with the same key recovers the
// ORIGINAL intent rather than creating a second one (§10.4).
func (c *PayinClient) CreateTopupIntent(ctx context.Context, tenantID uuid.UUID, environment, currency, amount, downstreamKey string) (PayinResult, error) {
	resp, err := c.grpc.CreateMerchantTopupIntent(ctx, &payinv1.CreateMerchantTopupIntentRequest{
		TenantId: tenantID.String(), Environment: environment, Currency: currency, Amount: amount, DownstreamKey: downstreamKey,
	})
	if err != nil {
		return PayinResult{}, translateError(err)
	}
	return payinResultFromProto(resp.GetIntent()), nil
}

// GetTopupIntent calls PayinService's GetMerchantTopupIntent, tenant-scoped
// (§7.3) — a topup intent owned by a different tenant returns the SAME
// ErrNotFound as one that doesn't exist at all (§6.7).
func (c *PayinClient) GetTopupIntent(ctx context.Context, tenantID, id uuid.UUID) (PayinResult, error) {
	resp, err := c.grpc.GetMerchantTopupIntent(ctx, &payinv1.GetMerchantTopupIntentRequest{TenantId: tenantID.String(), Id: id.String()})
	if err != nil {
		return PayinResult{}, translateError(err)
	}
	return payinResultFromProto(resp.GetIntent()), nil
}

func payinResultFromProto(intent *payinv1.TopupIntent) PayinResult {
	id, _ := uuid.Parse(intent.GetId())
	return PayinResult{
		ID: id, Status: intent.GetStatus(), Currency: intent.GetCurrency(), Amount: intent.GetAmount(),
		Vendor: intent.GetVendor(), CreatedAt: intent.GetCreatedAt().AsTime(), UpdatedAt: intent.GetUpdatedAt().AsTime(),
	}
}
