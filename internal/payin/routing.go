package payin

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ResolveTopupRoute picks the first candidate vendor (in routing-rule
// priority order) that is both registered and not circuit-broken
// (docs/plan/40 Task T2) — mirrors internal/payout's ResolvePayoutRoute.
func (m *Module) ResolveTopupRoute(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) (string, string, error) {
	candidates, err := m.routing.ResolveCandidates(ctx, "topup", userID, currency, amount.IntPart())
	if err != nil {
		return "", "", err
	}
	if len(candidates) == 0 {
		return "", "", ErrNoRoute
	}
	for _, c := range candidates {
		if _, ok := m.registry.Payin(c.Vendor); !ok {
			continue
		}
		if m.breaker != nil && !m.breaker.Allow(c.Vendor) {
			continue
		}
		return c.Vendor, c.Gateway, nil
	}
	return "", "", ErrNoVendorAvailable
}
