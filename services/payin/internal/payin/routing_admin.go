package payin

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	vendorgw "github.com/herdifirdausss/seev/contracts/vendorgw"
	"github.com/herdifirdausss/seev/internal/platform/database/identifiers"
	currencyreg "github.com/herdifirdausss/seev/internal/platform/money/currency"
	"github.com/herdifirdausss/seev/services/payin/internal/payin/model"
	"github.com/herdifirdausss/seev/services/payin/internal/repository"
)

// These aliases keep transport adapters independent from the persistence
// package. Repositories remain replaceable behind the service boundary.
var (
	ErrEventNotFound       = repository.ErrNotFound
	ErrRoutingRuleNotFound = repository.ErrNotFound
)

// RoutingRuleInput is the validated business input accepted by the admin
// routing surface. JSON decoding belongs to handler/; domain validation and
// rule creation belong here.
type RoutingRuleInput struct {
	Flow      string
	Priority  int
	Enabled   *bool
	Currency  *string
	MinAmount *int64
	MaxAmount *int64
	UserID    *uuid.UUID
	Vendor    string
}

// NewRoutingRule validates an admin routing request and assigns its durable
// identifier. It deliberately does not know anything about HTTP or JSON.
func (m *Module) NewRoutingRule(input RoutingRuleInput) (model.RoutingRule, error) {
	if input.Flow == "" {
		input.Flow = "topup"
	}
	if input.Flow != "topup" {
		return model.RoutingRule{}, errors.New("flow must be topup")
	}
	if input.Priority < 0 {
		return model.RoutingRule{}, errors.New("priority must be non-negative")
	}
	if input.Vendor == "" {
		return model.RoutingRule{}, errors.New("vendor is required")
	}
	if !m.IsPayinVendorRegistered(input.Vendor) {
		return model.RoutingRule{}, errors.New("vendor is not registered")
	}
	if input.MinAmount != nil && *input.MinAmount < 0 || input.MaxAmount != nil && *input.MaxAmount < 0 {
		return model.RoutingRule{}, errors.New("amount bounds must be non-negative")
	}
	if input.MinAmount != nil && input.MaxAmount != nil && *input.MinAmount > *input.MaxAmount {
		return model.RoutingRule{}, errors.New("min_amount must not exceed max_amount")
	}
	if input.Currency != nil {
		currency := strings.ToUpper(strings.TrimSpace(*input.Currency))
		if err := currencyreg.ValidateCode(currency); err != nil {
			return model.RoutingRule{}, errors.New("currency must be a three-letter uppercase code")
		}
		input.Currency = &currency
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	return model.RoutingRule{
		ID:        identifiers.NewV7(),
		Flow:      input.Flow,
		Priority:  input.Priority,
		Enabled:   enabled,
		Currency:  input.Currency,
		MinAmount: input.MinAmount,
		MaxAmount: input.MaxAmount,
		UserID:    input.UserID,
		Vendor:    input.Vendor,
	}, nil
}

func (m *Module) ListRoutingRules(ctx context.Context) ([]model.RoutingRule, error) {
	return m.routing.ListRules(ctx)
}

func (m *Module) CreateRoutingRule(ctx context.Context, rule model.RoutingRule) error {
	return m.routing.CreateRule(ctx, rule)
}

func (m *Module) UpdateRoutingRule(ctx context.Context, rule model.RoutingRule) error {
	return m.routing.UpdateRule(ctx, rule)
}

func (m *Module) GetVendorGateway(ctx context.Context, vendor string) (model.VendorGateway, bool, error) {
	return m.routing.GetVendorGateway(ctx, vendor)
}

func (m *Module) UpsertVendorGateway(ctx context.Context, mapping model.VendorGateway) error {
	return m.routing.UpsertVendorGateway(ctx, mapping)
}

func (m *Module) IsPayinVendorRegistered(vendor string) bool {
	return m.registry != nil && func() bool {
		_, ok := m.registry.Payin(vendor)
		return ok
	}()
}

func (m *Module) VendorHealth(ctx context.Context) []vendorgw.VendorHealth {
	if m.breaker == nil {
		return []vendorgw.VendorHealth{}
	}
	return m.breaker.Snapshot(ctx)
}

// ValidateVendorGateway keeps the set of supported gateway names in the
// service layer, alongside the routing policy rather than in an HTTP adapter.
func (m *Module) ValidateVendorGateway(vendor, gateway string) (model.VendorGateway, error) {
	if !m.IsPayinVendorRegistered(vendor) {
		return model.VendorGateway{}, errors.New("vendor is not registered")
	}
	switch gateway {
	case "bca", "gopay", "platform":
	default:
		return model.VendorGateway{}, errors.New("gateway is not allowed")
	}
	return model.VendorGateway{Vendor: vendor, Gateway: gateway}, nil
}
