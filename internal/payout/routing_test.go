package payout

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/vendorgw"
)

type matrixRouting struct{ rules []model.RoutingRule }

func (m matrixRouting) Resolve(_ context.Context, flow string, user uuid.UUID, currency string, amount int64) (model.RoutingRule, string, bool, error) {
	var matches []model.RoutingRule
	for _, r := range m.rules {
		if !r.Enabled || r.Flow != flow || r.UserID != nil && *r.UserID != user || r.Currency != nil && *r.Currency != currency || r.MinAmount != nil && amount < *r.MinAmount || r.MaxAmount != nil && amount > *r.MaxAmount {
			continue
		}
		matches = append(matches, r)
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
func (matrixRouting) UpsertVendorGateway(context.Context, model.VendorGateway) error { return nil }
func payoutString(v string) *string                                                  { return &v }
func payoutInt(v int64) *int64                                                       { return &v }
func TestResolvePayoutRouteMatrix(t *testing.T) {
	user := uuid.New()
	other := uuid.New()
	rules := []model.RoutingRule{{Flow: "payout", Priority: 1, Enabled: false, Vendor: "disabled"}, {Flow: "payout", Priority: 10, Enabled: true, Currency: payoutString("USD"), Vendor: "usd"}, {Flow: "payout", Priority: 20, Enabled: true, MinAmount: payoutInt(100), MaxAmount: payoutInt(200), Vendor: "range"}, {Flow: "payout", Priority: 999, Enabled: true, UserID: &user, Vendor: "user"}, {Flow: "payout", Priority: 1000, Enabled: true, Vendor: "fallback"}}
	registry := vendorgw.NewRegistry()
	for _, v := range []string{"usd", "range", "user", "fallback"} {
		registry.AddPayout(&stubPayoutProvider{name: v})
	}
	m := &Module{routing: matrixRouting{rules}, registry: registry}
	tests := []struct {
		name     string
		user     uuid.UUID
		currency string
		amount   int64
		want     string
	}{{"user override", user, "USD", 150, "user"}, {"currency", other, "USD", 500, "usd"}, {"range", other, "IDR", 150, "range"}, {"fallback", other, "IDR", 500, "fallback"}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vendor, _, err := m.ResolvePayoutRoute(context.Background(), tc.user, tc.currency, decimal.NewFromInt(tc.amount))
			require.NoError(t, err)
			assert.Equal(t, tc.want, vendor)
		})
	}
	m.routing = matrixRouting{}
	_, _, err := m.ResolvePayoutRoute(context.Background(), other, "IDR", decimal.NewFromInt(1))
	assert.ErrorIs(t, err, ErrNoRoute)
}
