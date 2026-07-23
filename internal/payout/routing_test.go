package payout

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/vendorgw"
)

type matrixRouting struct{ rules []model.RoutingRule }

func (m matrixRouting) ResolveCandidates(_ context.Context, flow string, user uuid.UUID, currency string, amount int64) ([]model.RoutingCandidate, error) {
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
	out := make([]model.RoutingCandidate, len(matches))
	for i, r := range matches {
		out[i] = model.RoutingCandidate{Vendor: r.Vendor, Gateway: r.Vendor + "-gateway"}
	}
	return out, nil
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
			vendor, _, err := m.ResolvePayoutRoute(context.Background(), tc.user, tc.currency, decimal.NewFromInt(tc.amount), nil)
			require.NoError(t, err)
			assert.Equal(t, tc.want, vendor)
		})
	}
	m.routing = matrixRouting{}
	_, _, err := m.ResolvePayoutRoute(context.Background(), other, "IDR", decimal.NewFromInt(1), nil)
	assert.ErrorIs(t, err, ErrNoRoute)
}

// TestResolvePayoutRoute_BreakerOpen_SkipsToNextCandidate is docs/roadmap/archive/40
// Task T2's own "Required test": a vendor whose circuit is open must be
// skipped in favor of the next candidate in priority order.
func TestResolvePayoutRoute_BreakerOpen_SkipsToNextCandidate(t *testing.T) {
	rules := []model.RoutingRule{
		{Flow: "payout", Priority: 1, Enabled: true, Vendor: "primary"},
		{Flow: "payout", Priority: 2, Enabled: true, Vendor: "secondary"},
	}
	registry := vendorgw.NewRegistry()
	registry.AddPayout(&stubPayoutProvider{name: "primary"})
	registry.AddPayout(&stubPayoutProvider{name: "secondary"})
	breaker := vendorgw.NewHealthTracker(1, time.Hour, nil)
	breaker.RecordFailure(context.Background(), "primary") // trips open at threshold 1

	m := &Module{routing: matrixRouting{rules}, registry: registry, breaker: breaker}
	vendor, _, err := m.ResolvePayoutRoute(context.Background(), uuid.New(), "IDR", decimal.NewFromInt(1), nil)
	require.NoError(t, err)
	assert.Equal(t, "secondary", vendor, "the open-circuit primary must be skipped in favor of secondary")
}

// TestResolvePayoutRoute_AllCandidatesOpen_ErrNoVendorAvailable proves the
// distinct error (503 VENDOR_UNAVAILABLE at the gateway) from "no rule
// matched at all" (ErrNoRoute).
func TestResolvePayoutRoute_AllCandidatesOpen_ErrNoVendorAvailable(t *testing.T) {
	rules := []model.RoutingRule{{Flow: "payout", Priority: 1, Enabled: true, Vendor: "primary"}}
	registry := vendorgw.NewRegistry()
	registry.AddPayout(&stubPayoutProvider{name: "primary"})
	breaker := vendorgw.NewHealthTracker(1, time.Hour, nil)
	breaker.RecordFailure(context.Background(), "primary")

	m := &Module{routing: matrixRouting{rules}, registry: registry, breaker: breaker}
	_, _, err := m.ResolvePayoutRoute(context.Background(), uuid.New(), "IDR", decimal.NewFromInt(1), nil)
	assert.ErrorIs(t, err, ErrNoVendorAvailable)
}

// TestResolvePayoutRoute_ExclusionList_SkipsAlreadyTried is docs/roadmap/archive/40
// Task T3's failover mechanism at the routing layer: a vendor named in
// exclude is skipped even though its circuit is closed.
func TestResolvePayoutRoute_ExclusionList_SkipsAlreadyTried(t *testing.T) {
	rules := []model.RoutingRule{
		{Flow: "payout", Priority: 1, Enabled: true, Vendor: "primary"},
		{Flow: "payout", Priority: 2, Enabled: true, Vendor: "secondary"},
	}
	registry := vendorgw.NewRegistry()
	registry.AddPayout(&stubPayoutProvider{name: "primary"})
	registry.AddPayout(&stubPayoutProvider{name: "secondary"})

	m := &Module{routing: matrixRouting{rules}, registry: registry}
	vendor, _, err := m.ResolvePayoutRoute(context.Background(), uuid.New(), "IDR", decimal.NewFromInt(1), []string{"primary"})
	require.NoError(t, err)
	assert.Equal(t, "secondary", vendor, "primary must be skipped — it's in the exclusion list")
}
