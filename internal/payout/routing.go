package payout

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func (m *Module) ResolvePayoutRoute(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) (string, string, error) {
	rule, gateway, found, err := m.routing.Resolve(ctx, "payout", userID, currency, amount.IntPart())
	if err != nil {
		return "", "", err
	}
	if !found {
		return "", "", ErrNoRoute
	}
	if _, ok := m.registry.Payout(rule.Vendor); !ok {
		return "", "", fmt.Errorf("payout: routed vendor %q is not registered", rule.Vendor)
	}
	return rule.Vendor, gateway, nil
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
