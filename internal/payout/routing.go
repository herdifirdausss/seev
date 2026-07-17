package payout

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ResolvePayoutRoute picks the first candidate vendor (in routing-rule
// priority order) that is both registered and not circuit-broken, skipping
// any vendor named in exclude (docs/plan/40 Task T3's failover — vendors
// already tried for this request). Pass a nil/empty exclude for a fresh
// request.
func (m *Module) ResolvePayoutRoute(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal, exclude []string) (string, string, error) {
	candidates, err := m.routing.ResolveCandidates(ctx, "payout", userID, currency, amount.IntPart())
	if err != nil {
		return "", "", err
	}
	if len(candidates) == 0 {
		return "", "", ErrNoRoute
	}
	excluded := make(map[string]bool, len(exclude))
	for _, v := range exclude {
		excluded[v] = true
	}
	for _, c := range candidates {
		if excluded[c.Vendor] {
			continue
		}
		if _, ok := m.registry.Payout(c.Vendor); !ok {
			continue
		}
		if m.breaker != nil && !m.breaker.Allow(c.Vendor) {
			continue
		}
		return c.Vendor, c.Gateway, nil
	}
	return "", "", ErrNoVendorAvailable
}

func (m *Module) gatewayForVendor(ctx context.Context, vendor string) (string, error) {
	mapping, found, err := m.routing.GetVendorGateway(ctx, vendor)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("payout: vendor %q has no gateway mapping configured", vendor)
	}
	return mapping.Gateway, nil
}
