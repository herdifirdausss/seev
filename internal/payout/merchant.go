package payout

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/payout/repository"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	currencyreg "github.com/herdifirdausss/seev/pkg/currency"
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
//
// downstreamKey (B2B HTTP handlers follow-up to Plan 57 T6) is Gateway's
// own idempotency.DownstreamKey for this (tenant, operation, merchant
// idempotency key) — required, non-empty. A Gateway retry (including one
// caused by a Gateway crash after this call already succeeded once, before
// its own idempotency record was persisted — docs/reference/c1-b2b-design.md
// §10.4) reuses the SAME downstreamKey, so InsertMerchant returns the
// ORIGINAL request instead of holding funds a second time.
func (m *Module) CreateMerchant(ctx context.Context, tenantID uuid.UUID, environment, currency string, amount decimal.Decimal, destination []byte, createdBy, downstreamKey string) (uuid.UUID, error) {
	if tenantID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("payout: tenantID is required")
	}
	if downstreamKey == "" {
		return uuid.Nil, fmt.Errorf("payout: downstreamKey is required")
	}
	if err := currencyreg.ValidatePositiveMinorAmount(amount); err != nil {
		return uuid.Nil, fmt.Errorf("%w: %v", ErrInvalidAmount, err)
	}
	if err := m.ensureIntakeOpen(ctx); err != nil {
		return uuid.Nil, err
	}
	if err := currencyreg.ValidateCode(currency); err != nil {
		return uuid.Nil, fmt.Errorf("payout: invalid currency: %w", err)
	}
	if validator, ok := m.poster.(interface {
		ValidateCurrency(context.Context, string, string) error
	}); ok {
		if err := validator.ValidateCurrency(ctx, currency, "payout"); err != nil {
			return uuid.Nil, fmt.Errorf("payout: currency policy: %w", err)
		}
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
		RequestID:     middleware.RequestIDFromCtx(ctx),
		DownstreamKey: downstreamKey,
	}
	stored, err := m.repo.InsertMerchant(ctx, req)
	if err != nil {
		return uuid.Nil, fmt.Errorf("payout: insert merchant request: %w", err)
	}
	if stored.ID != id {
		// A prior attempt for the same downstreamKey already won — its
		// hold (or later state) is the authoritative one; this attempt
		// must not also place a second hold for the same logical request.
		return stored.ID, nil
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

// GetMerchant is the tenant-scoped counterpart of Get (§7.3: every
// merchant-owned read must be scoped by tenant_id). A payout request that
// exists but belongs to a different tenant returns the same
// repository.ErrNotFound as a genuinely missing one — §6.7's "never leak
// resource existence across tenants."
func (m *Module) GetMerchant(ctx context.Context, tenantID, id uuid.UUID) (model.PayoutRequest, error) {
	req, err := m.Get(ctx, id)
	if err != nil {
		return model.PayoutRequest{}, err
	}
	if req.MerchantTenantID != tenantID {
		return model.PayoutRequest{}, repository.ErrNotFound
	}
	return req, nil
}

// resolveMerchantVendor implements sandbox-to-mock routing (Plan 57 T6) —
// mirrors internal/payin's own resolveMerchantVendor exactly. sandbox:
// sandboxVendor, unconditionally, or ErrSandboxVendorUnavailable if it
// isn't registered — never falls through to rule-based resolution. live:
// the existing rule-based ResolvePayoutRoute, unchanged, with no user-id
// override and no exclusion list (a fresh request).
func (m *Module) resolveMerchantVendor(ctx context.Context, environment, currency string, amount decimal.Decimal) (string, error) {
	if environment == "sandbox" {
		vendor, ok := m.registry.Payout(sandboxVendor)
		if !ok {
			return "", ErrSandboxVendorUnavailable
		}
		if !vendorgw.SupportsRequestedCurrency(vendor, "payout", currency) {
			return "", ErrCurrencyRouteUnavailable
		}
		return sandboxVendor, nil
	}
	vendor, _, err := m.ResolvePayoutRoute(ctx, uuid.Nil, currency, amount, nil)
	if err != nil {
		return "", err
	}
	return vendor, nil
}
