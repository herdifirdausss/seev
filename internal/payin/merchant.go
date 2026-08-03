package payin

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	currencyreg "github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

// sandboxVendor is the ONLY vendor a sandbox-environment merchant tenant
// may ever be routed to (Plan 57 T6 — "sandbox-to-mock routing"). This is
// a structural guarantee, not a routing-rule preference: a sandbox
// request never even calls ResolveTopupRoute, so a future rule-authoring
// mistake in payin_routing_rules can never accidentally route a sandbox
// tenant to a live-capable vendor. Mirrors merchant_transfer's own
// "source is structurally never caller-supplied" defense-in-depth
// philosophy (internal/ledger/processors/merchant_transfer.go).
const sandboxVendor = "mockvendor"

// CreateMerchantTopupIntent starts a merchant-initiated pay-in (Plan 57
// T6) — the merchant-owned counterpart of CreateTopupIntent. currency is
// caller-supplied (the B2B CreatePayinRequest contract's own field),
// unlike the user path, which resolves it from the user's own cash
// account; a merchant tenant may not yet have specified which pocket/cash
// account currency applies, so B2B Gateway supplies it directly.
//
// environment ("sandbox" | "live") comes from the authenticated
// principal (Gateway's own resolved API key environment, never a
// caller-suppliable request field) and determines routing: sandbox is
// ALWAYS routed to sandboxVendor, structurally, before any routing-rule
// resolution ever runs.
//
// downstreamKey (B2B HTTP handlers follow-up to Plan 57 T6) is Gateway's
// own idempotency.DownstreamKey for this (tenant, operation, merchant
// idempotency key) — required, non-empty. A Gateway retry (including one
// caused by a Gateway crash after this call already succeeded once, before
// its own idempotency record was persisted — docs/reference/c1-b2b-design.md
// §10.4) reuses the SAME downstreamKey, so InsertMerchantTopupIntent
// returns the ORIGINAL intent instead of creating a second one.
func (m *Module) CreateMerchantTopupIntent(ctx context.Context, tenantID uuid.UUID, environment, currency string, amount decimal.Decimal, downstreamKey string) (TopupIntent, error) {
	if tenantID == uuid.Nil {
		return TopupIntent{}, fmt.Errorf("payin: tenantID is required")
	}
	if downstreamKey == "" {
		return TopupIntent{}, fmt.Errorf("payin: downstreamKey is required")
	}
	if err := currencyreg.ValidatePositiveMinorAmount(amount); err != nil {
		return TopupIntent{}, fmt.Errorf("%w: %v", ErrInvalidAmount, err)
	}
	if err := m.ensureIntakeOpen(ctx); err != nil {
		return TopupIntent{}, err
	}
	if err := currencyreg.ValidateCode(currency); err != nil {
		return TopupIntent{}, fmt.Errorf("payin: invalid currency: %w", err)
	}
	if validator, ok := m.poster.(interface {
		ValidateCurrency(context.Context, string, string) error
	}); ok {
		if err := validator.ValidateCurrency(ctx, currency, "topup"); err != nil {
			return TopupIntent{}, fmt.Errorf("payin: currency policy: %w", err)
		}
	}

	vendor, err := m.resolveMerchantVendor(ctx, environment, currency, amount)
	if err != nil {
		return TopupIntent{}, err
	}

	ttl := m.topupTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	intent := model.TopupIntent{
		ID:               generalutil.NewV7(),
		Reference:        "TOP-" + generalutil.NewV7().String(),
		MerchantTenantID: tenantID,
		Amount:           amount,
		Currency:         currency,
		Vendor:           vendor,
		Status:           model.TopupStatusPending,
		ExpiresAt:        time.Now().Add(ttl),
		RequestID:        middleware.RequestIDFromCtx(ctx),
		DownstreamKey:    downstreamKey,
	}
	stored, err := m.repo.InsertMerchantTopupIntent(ctx, intent)
	if err != nil {
		return TopupIntent{}, fmt.Errorf("payin: insert merchant topup intent: %w", err)
	}
	if stored.ID != intent.ID {
		// A prior attempt for the same downstreamKey already won — return
		// its resource unchanged; this attempt must not also open a vendor
		// session for a topup intent nobody else will ever settle against.
		return stored, nil
	}
	if m.vendorSession != nil {
		if err := m.vendorSession.CreatePayinSession(ctx, vendor, intent.ID.String(), amount, currency, intent.RequestID); err != nil {
			return TopupIntent{}, fmt.Errorf("payin: create VendorService session: %w", err)
		}
	}
	return stored, nil
}

// GetMerchantTopupIntent is the tenant-scoped counterpart of GetTopupIntent
// (§7.3: every merchant-owned read must be scoped by tenant_id). A topup
// intent that exists but belongs to a different tenant returns the same
// ErrTopupIntentNotFound as a genuinely missing one — §6.7's "never leak
// resource existence across tenants."
func (m *Module) GetMerchantTopupIntent(ctx context.Context, tenantID, id uuid.UUID) (TopupIntent, error) {
	intent, err := m.GetTopupIntent(ctx, id)
	if err != nil {
		return TopupIntent{}, err
	}
	if intent.MerchantTenantID != tenantID {
		return TopupIntent{}, ErrTopupIntentNotFound
	}
	return intent, nil
}

// resolveMerchantVendor implements sandbox-to-mock routing (Plan 57 T6).
// sandbox: sandboxVendor, unconditionally, or ErrSandboxVendorUnavailable
// if it isn't registered — never falls through to rule-based resolution.
// live: the existing rule-based ResolveTopupRoute, unchanged, with no
// user-id override (uuid.Nil — a merchant has no per-tenant routing rule
// in this MVP, only the wildcard rules every user already shares).
func (m *Module) resolveMerchantVendor(ctx context.Context, environment, currency string, amount decimal.Decimal) (string, error) {
	if environment == "sandbox" {
		vendor, ok := m.registry.Payin(sandboxVendor)
		if !ok {
			return "", ErrSandboxVendorUnavailable
		}
		if !vendorgw.SupportsRequestedCurrency(vendor, "topup", currency) {
			return "", ErrCurrencyRouteUnavailable
		}
		return sandboxVendor, nil
	}
	vendor, _, err := m.ResolveTopupRoute(ctx, uuid.Nil, currency, amount)
	if err != nil {
		return "", err
	}
	return vendor, nil
}
