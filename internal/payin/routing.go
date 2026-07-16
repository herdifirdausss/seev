package payin

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func (m *Module) ResolveTopupRoute(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) (string, string, error) {
	rule, gateway, found, err := m.routing.Resolve(ctx, "topup", userID, currency, amount.IntPart())
	if err != nil {
		return "", "", err
	}
	if !found {
		return "", "", ErrNoRoute
	}
	if _, ok := m.registry.Payin(rule.Vendor); !ok {
		return "", "", fmt.Errorf("payin: routed vendor %q is not registered", rule.Vendor)
	}
	return rule.Vendor, gateway, nil
}
