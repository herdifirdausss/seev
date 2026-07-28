package payout

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

// sandboxVendor is the ONLY vendor a sandbox-environment merchant tenant
// may ever be routed to (Plan 57 T6) — mirrors internal/payin's own
// sandboxVendor constant and the same structural (not rule-based)
// enforcement rationale.
const sandboxVendor = "mockvendor"

// CreateMerchant starts a merchant-owned payout (Plan 57 T6) — the
// merchant-owned counterpart of Create. currency is caller-supplied (the
// B2B CreatePayoutRequest contract's own field), unlike the user path,
// which resolves it from the user's own cash account. Fee-quote
// consumption is not offered on this path (no quote_id field exists on
// the B2B contract) — settle() falls back to ResolveFee exactly as any
// unquoted user payout already does.
//
// environment ("sandbox" | "live") comes from the authenticated
// principal (Gateway's own resolved API key environment, never a
// caller-suppliable request field) and determines routing exactly like
// internal/payin's CreateMerchantTopupIntent.
func (m *Module) CreateMerchant(ctx context.Context, tenantID uuid.UUID, environment, currency string, amount decimal.Decimal, destination []byte, createdBy string) (uuid.UUID, error) {
	if tenantID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("payout: tenantID is required")
	}
	if err := m.ensureIntakeOpen(ctx); err != nil {
		return uuid.Nil, err
	}

	// Fraud screening is deliberately SKIPPED for merchant-owned payouts
	// (Plan 57 T6 scope decision) — same reasoning as internal/payin's own
	// CreateMerchantTopupIntent: fraudClient.Check is keyed on a single
	// userID, and a merchant tenant has none; running it unmodified would
	// silently pool every merchant tenant into one shared "zero user"
	// velocity bucket. Merchant-specific fraud/velocity screening is out
	// of scope for T6.

	vendor, err := m.resolveMerchantVendor(ctx, environment, currency, amount)
	if err != nil {
		return uuid.Nil, err
	}

	id := generalutil.NewV7()
	req := model.PayoutRequest{
		ID: id, MerchantTenantID: tenantID, Amount: amount, Currency: currency,
		Vendor: vendor, Destination: destination, CreatedBy: createdBy,
		RequestID: middleware.RequestIDFromCtx(ctx),
	}
	if err := m.repo.Insert(ctx, req); err != nil {
		return uuid.Nil, fmt.Errorf("payout: insert merchant request: %w", err)
	}

	if err := m.hold(ctx, req); err != nil {
		return id, fmt.Errorf("payout: hold: %w", err)
	}

	if err := m.enqueueSubmit(ctx, id, vendor); err != nil {
		// Hold already succeeded — money is safely parked in the tenant's
		// hold account. An enqueue failure here is not a CreateMerchant-level
		// error; the resume job retries the enqueue (idempotent), same as
		// the user path.
		m.logger.Error("payout: initial enqueue submit failed, resume job will retry",
			slog.Any("error", err), slog.String("request_id", id.String()))
	}

	return id, nil
}

// resolveMerchantVendor implements sandbox-to-mock routing (Plan 57 T6) —
// mirrors internal/payin's own resolveMerchantVendor exactly. sandbox:
// sandboxVendor, unconditionally, or ErrSandboxVendorUnavailable if it
// isn't registered — never falls through to rule-based resolution. live:
// the existing rule-based ResolvePayoutRoute, unchanged, with no user-id
// override and no exclusion list (a fresh request).
func (m *Module) resolveMerchantVendor(ctx context.Context, environment, currency string, amount decimal.Decimal) (string, error) {
	if environment == "sandbox" {
		if _, ok := m.registry.Payout(sandboxVendor); !ok {
			return "", ErrSandboxVendorUnavailable
		}
		return sandboxVendor, nil
	}
	vendor, _, err := m.ResolvePayoutRoute(ctx, uuid.Nil, currency, amount, nil)
	if err != nil {
		return "", err
	}
	return vendor, nil
}
