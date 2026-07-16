package payin

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/internal/vendorgw"
)

type matrixRouting struct{ rules []model.RoutingRule }

func (m matrixRouting) Resolve(_ context.Context, flow string, userID uuid.UUID, currency string, amount int64) (model.RoutingRule, string, bool, error) {
	var matches []model.RoutingRule
	for _, rule := range m.rules {
		if !rule.Enabled || rule.Flow != flow || rule.UserID != nil && *rule.UserID != userID || rule.Currency != nil && *rule.Currency != currency || rule.MinAmount != nil && amount < *rule.MinAmount || rule.MaxAmount != nil && amount > *rule.MaxAmount {
			continue
		}
		matches = append(matches, rule)
	}
	sort.Slice(matches, func(i, j int) bool {
		if (matches[i].UserID != nil) != (matches[j].UserID != nil) {
			return matches[i].UserID != nil
		}
		return matches[i].Priority < matches[j].Priority
	})
	if len(matches) == 0 {
		return model.RoutingRule{}, "", false, nil
	}
	return matches[0], matches[0].Vendor + "-gateway", true, nil
}
func (m matrixRouting) ListRules(context.Context) ([]model.RoutingRule, error) { return m.rules, nil }
func (matrixRouting) CreateRule(context.Context, model.RoutingRule) error      { return nil }
func (matrixRouting) UpdateRule(context.Context, model.RoutingRule) error      { return nil }
func (matrixRouting) GetVendorGateway(context.Context, string) (model.VendorGateway, bool, error) {
	return model.VendorGateway{}, false, nil
}
func (matrixRouting) ListVendorGateways(context.Context) ([]model.VendorGateway, error) {
	return nil, nil
}
func (matrixRouting) UpsertVendorGateway(context.Context, model.VendorGateway) error { return nil }

func stringPtr(v string) *string { return &v }
func int64Ptr(v int64) *int64    { return &v }

func TestResolveTopupRoute_Matrix(t *testing.T) {
	userID := uuid.New()
	otherUser := uuid.New()
	rules := []model.RoutingRule{
		{Flow: "topup", Priority: 1, Enabled: false, Vendor: "disabled"},
		{Flow: "topup", Priority: 10, Enabled: true, Currency: stringPtr("USD"), Vendor: "usd"},
		{Flow: "topup", Priority: 20, Enabled: true, MinAmount: int64Ptr(100), MaxAmount: int64Ptr(200), Vendor: "range"},
		{Flow: "topup", Priority: 999, Enabled: true, UserID: &userID, Vendor: "user"},
		{Flow: "topup", Priority: 1000, Enabled: true, Vendor: "fallback"},
	}
	registry := vendorgw.NewRegistry()
	for _, vendor := range []string{"usd", "range", "user", "fallback"} {
		registry.AddPayin(stubVerifier{name: vendor})
	}
	m := &Module{routing: matrixRouting{rules: rules}, registry: registry}

	tests := []struct {
		name     string
		user     uuid.UUID
		currency string
		amount   int64
		want     string
	}{
		{"user override beats lower global priority", userID, "USD", 150, "user"},
		{"currency filter", otherUser, "USD", 500, "usd"},
		{"amount range", otherUser, "IDR", 150, "range"},
		{"fallback", otherUser, "IDR", 500, "fallback"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vendor, _, err := m.ResolveTopupRoute(context.Background(), tc.user, tc.currency, decimal.NewFromInt(tc.amount))
			require.NoError(t, err)
			assert.Equal(t, tc.want, vendor)
		})
	}

	noRoute := &Module{routing: matrixRouting{}, registry: registry}
	_, _, err := noRoute.ResolveTopupRoute(context.Background(), otherUser, "IDR", decimal.NewFromInt(1))
	assert.ErrorIs(t, err, ErrNoRoute)
}
