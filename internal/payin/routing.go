package payin

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/loadmetrics"
	"github.com/shopspring/decimal"
)

// ResolveTopupRoute picks the first candidate vendor (in routing-rule
// priority order) that is both registered and not circuit-broken
// (docs/roadmap/archive/40 Task T2) — mirrors internal/payout's ResolvePayoutRoute.
func (m *Module) ResolveTopupRoute(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) (string, string, error) {
	started := time.Now()
	result := "not_found"
	defer func() { loadmetrics.ObserveResolution("payin", "payin_routing", result, started) }()
	candidates, err := m.routing.ResolveCandidates(ctx, "topup", userID, currency, amount.IntPart())
	if err != nil {
		result = "error"
		return "", "", err
	}
	if len(candidates) == 0 {
		return "", "", ErrNoRoute
	}
	sawCurrencyMismatch := false
	sawCurrencyCapable := false
	for _, c := range candidates {
		vendor, ok := m.registry.Payin(c.Vendor)
		if !ok {
			continue
		}
		if !vendorgw.SupportsRequestedCurrency(vendor, "topup", currency) {
			sawCurrencyMismatch = true
			continue
		}
		sawCurrencyCapable = true
		if m.breaker != nil && !m.breaker.Allow(ctx, c.Vendor) {
			continue
		}
		return c.Vendor, c.Gateway, nil
	}
	if currency != "" && currency != "IDR" && sawCurrencyMismatch && !sawCurrencyCapable {
		return "", "", ErrCurrencyRouteUnavailable
	}
	return "", "", ErrNoVendorAvailable
}
