package payout

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	vendorgw "github.com/herdifirdausss/seev/contracts/vendorgw"
	"github.com/herdifirdausss/seev/internal/platform/database/identifiers"
	currencyreg "github.com/herdifirdausss/seev/internal/platform/money/currency"
	"github.com/herdifirdausss/seev/services/payout/internal/payout/model"
	"github.com/herdifirdausss/seev/services/payout/internal/repository"
)

var (
	ErrRequestNotFound       = repository.ErrNotFound
	ErrVendorCommandNotFound = repository.ErrCommandNotFound
	ErrForceFailUnsupported  = errors.New("payout: vendor does not support force-fail")
)

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

func (m *Module) NewRoutingRule(input RoutingRuleInput) (model.RoutingRule, error) {
	if input.Flow == "" {
		input.Flow = "payout"
	}
	if input.Flow != "payout" {
		return model.RoutingRule{}, errors.New("flow must be payout")
	}
	if input.Priority < 0 {
		return model.RoutingRule{}, errors.New("priority must be non-negative")
	}
	if input.Vendor == "" {
		return model.RoutingRule{}, errors.New("vendor is required")
	}
	if !m.IsPayoutVendorRegistered(input.Vendor) {
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
	return model.RoutingRule{ID: identifiers.NewV7(), Flow: input.Flow, Priority: input.Priority, Enabled: enabled, Currency: input.Currency, MinAmount: input.MinAmount, MaxAmount: input.MaxAmount, UserID: input.UserID, Vendor: input.Vendor}, nil
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

func (m *Module) ValidateVendorGateway(vendor, gateway string) (model.VendorGateway, error) {
	if !m.IsPayoutVendorRegistered(vendor) {
		return model.VendorGateway{}, errors.New("vendor is not registered")
	}
	switch gateway {
	case "bca", "gopay", "platform":
	default:
		return model.VendorGateway{}, errors.New("gateway is not allowed")
	}
	return model.VendorGateway{Vendor: vendor, Gateway: gateway}, nil
}

func (m *Module) IsPayoutVendorRegistered(vendor string) bool {
	if m.registry == nil {
		return false
	}
	_, ok := m.registry.Payout(vendor)
	return ok
}

func (m *Module) VendorHealth(ctx context.Context) []vendorgw.VendorHealth {
	if m.breaker == nil {
		return []vendorgw.VendorHealth{}
	}
	return m.breaker.Snapshot(ctx)
}

func (m *Module) ForceFailVendor(vendor string, fail bool) error {
	if m.registry == nil {
		return ErrUnknownVendor
	}
	provider, ok := m.registry.Payout(vendor)
	if !ok {
		return ErrUnknownVendor
	}
	switcher, ok := provider.(interface{ SetForceFail(bool) })
	if !ok {
		return ErrForceFailUnsupported
	}
	switcher.SetForceFail(fail)
	return nil
}

func (m *Module) ListDeadVendorCommands(ctx context.Context, limit, offset int) ([]model.PayoutVendorCommand, error) {
	return m.commandRepo.ListDeadCommands(ctx, limit, offset)
}

func (m *Module) ReplayDeadVendorCommand(ctx context.Context, id uuid.UUID) error {
	return m.commandRepo.ReplayDeadCommand(ctx, id)
}

func (m *Module) ReplayAllDeadVendorCommands(ctx context.Context, olderThan time.Time) (int, error) {
	return m.commandRepo.ReplayAllDeadCommands(ctx, olderThan)
}
