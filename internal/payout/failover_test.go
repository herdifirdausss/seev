package payout

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/payout/repository"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"go.uber.org/mock/gomock"
)

// multiVendorRouting is a routing stub for docs/plan/40 Task T3's
// multi-vendor failover tests — matrixRouting (routing_test.go) also lives
// in this package but its GetVendorGateway always reports not-found, which
// breaks submit()'s gatewayForVendor call for a SECOND vendor; this stub
// serves a real gateway for every vendor named in candidates.
type multiVendorRouting struct {
	candidates []model.RoutingCandidate
}

func (r multiVendorRouting) ResolveCandidates(context.Context, string, uuid.UUID, string, int64) ([]model.RoutingCandidate, error) {
	return r.candidates, nil
}
func (multiVendorRouting) ListRules(context.Context) ([]model.RoutingRule, error) { return nil, nil }
func (multiVendorRouting) CreateRule(context.Context, model.RoutingRule) error    { return nil }
func (multiVendorRouting) UpdateRule(context.Context, model.RoutingRule) error    { return nil }
func (r multiVendorRouting) GetVendorGateway(_ context.Context, vendor string) (model.VendorGateway, bool, error) {
	for _, c := range r.candidates {
		if c.Vendor == vendor {
			return model.VendorGateway{Vendor: vendor, Gateway: c.Gateway}, true, nil
		}
	}
	return model.VendorGateway{}, false, nil
}
func (multiVendorRouting) UpsertVendorGateway(context.Context, model.VendorGateway) error { return nil }

// TestSubmit_VendorRejectsSynchronously_FailsOverToNextCandidate is
// docs/plan/40 Task T3's key scenario (a): vendor A rejects instantly, B
// succeeds — exactly one settle, and the request's vendor column is
// updated to reflect the winner.
func TestSubmit_VendorRejectsSynchronously_FailsOverToNextCandidate(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	holdTxID := uuid.New()
	settleTxID := uuid.New()

	req := sampleRequest(id, model.StatusHeld)
	req.HoldTxID = &holdTxID
	req.Vendor = "vendorA"

	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil).Times(2)
	repo.EXPECT().TransitionToSubmitted(gomock.Any(), id).Return(true, nil)
	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	repo.EXPECT().ListVendorCalls(gomock.Any(), id).Return(nil, nil) // no prior committed outcome — failover allowed
	repo.EXPECT().SetVendor(gomock.Any(), id, "vendorB").Return(nil)
	repo.EXPECT().TransitionToSettled(gomock.Any(), id, settleTxID).Return(true, nil)

	providerA := &stubPayoutProvider{name: "vendorA", submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
		return vendorgw.PayoutResult{Status: vendorgw.PayoutFailed, Reason: "declined by vendorA"}, nil
	}}
	providerB := &stubPayoutProvider{name: "vendorB", submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
		return vendorgw.PayoutResult{Status: vendorgw.PayoutSettled}, nil
	}}
	registry := vendorgw.NewRegistry()
	registry.AddPayout(providerA)
	registry.AddPayout(providerB)

	routing := multiVendorRouting{candidates: []model.RoutingCandidate{
		{Vendor: "vendorA", Gateway: "bca"},
		{Vendor: "vendorB", Gateway: "bri"},
	}}

	poster := stubPoster{
		postFn: func(context.Context, ledgerclient.Command) error { return nil },
		getTxFn: func(context.Context, string, string) (ledgerclient.Transaction, error) {
			return ledgerclient.Transaction{ID: settleTxID}, nil
		},
	}

	m := &Module{repo: repo, poster: poster, registry: registry, routing: routing, logger: discardLogger()}
	err := m.submit(context.Background(), id)
	require.NoError(t, err)

	assert.Equal(t, int64(1), providerA.submitted.Load(), "A must be tried exactly once")
	assert.Equal(t, int64(1), providerB.submitted.Load(), "B must be tried exactly once")
}

// TestSubmit_VendorTimesOut_NeverFailsOver_PinnedForResume is docs/plan/40
// Task T3's scenario (b): a timeout (infra error, not a business
// rejection) must NEVER trigger failover — the request stays 'submitted',
// pinned to the SAME vendor, for the resume job to retry.
func TestSubmit_VendorTimesOut_NeverFailsOver_PinnedForResume(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	holdTxID := uuid.New()

	req := sampleRequest(id, model.StatusHeld)
	req.HoldTxID = &holdTxID
	req.Vendor = "vendorA"

	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil)
	repo.EXPECT().TransitionToSubmitted(gomock.Any(), id).Return(true, nil)
	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().SetError(gomock.Any(), id, gomock.Any()).Return(nil)
	// Crucially: NO ListVendorCalls, NO SetVendor, NO TransitionToCancelled
	// call is expected — an uncertain outcome short-circuits straight out
	// of submit() without ever consulting mayFailover.

	providerA := &stubPayoutProvider{name: "vendorA", submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
		return vendorgw.PayoutResult{}, assertAnErr("mockvendor: submit timed out (simulated)")
	}}
	registry := vendorgw.NewRegistry()
	registry.AddPayout(providerA)
	// A second vendor IS registered/routable — proving the timeout path
	// never even consults it.
	registry.AddPayout(&stubPayoutProvider{name: "vendorB"})

	routing := multiVendorRouting{candidates: []model.RoutingCandidate{
		{Vendor: "vendorA", Gateway: "bca"},
		{Vendor: "vendorB", Gateway: "bri"},
	}}

	m := &Module{repo: repo, poster: stubPoster{}, registry: registry, routing: routing, logger: discardLogger()}
	err := m.submit(context.Background(), id)
	require.Error(t, err)
	assert.Equal(t, int64(1), providerA.submitted.Load())
}

func TestSubmit_VendorCallPersistenceFailureRefusesProgress(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	holdTxID := uuid.New()

	req := sampleRequest(id, model.StatusHeld)
	req.HoldTxID = &holdTxID
	req.Vendor = "vendorA"

	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil)
	repo.EXPECT().TransitionToSubmitted(gomock.Any(), id).Return(true, nil)
	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(assertAnErr("database unavailable"))
	// No settlement, cancellation, failover lookup, or vendor replacement is
	// allowed after the durable call history fails to record the outcome.

	providerA := &stubPayoutProvider{name: "vendorA", submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
		return vendorgw.PayoutResult{Status: vendorgw.PayoutSettled}, nil
	}}
	registry := vendorgw.NewRegistry()
	registry.AddPayout(providerA)

	m := &Module{repo: repo, poster: stubPoster{}, registry: registry, logger: discardLogger()}
	err := m.submit(context.Background(), id)
	require.ErrorContains(t, err, "persist vendor call outcome")
	assert.Equal(t, int64(1), providerA.submitted.Load())
}

func TestSubmit_ConcurrentAcceptedCallPreventsCancellation(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()

	req := sampleRequest(id, model.StatusHeld)
	req.Vendor = "vendorA"

	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil)
	repo.EXPECT().TransitionToSubmitted(gomock.Any(), id).Return(true, nil)
	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().ListVendorCalls(gomock.Any(), id).Return([]model.PayoutVendorCall{
		{Outcome: model.VendorCallRejected},
		{Outcome: model.VendorCallAccepted},
	}, nil)
	// No routing, vendor replacement, or cancellation is allowed once a
	// concurrent attempt has recorded an accepted outcome.

	providerA := &stubPayoutProvider{name: "vendorA", submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
		return vendorgw.PayoutResult{Status: vendorgw.PayoutFailed, Reason: "declined"}, nil
	}}
	registry := vendorgw.NewRegistry()
	registry.AddPayout(providerA)

	m := &Module{repo: repo, poster: stubPoster{}, registry: registry, logger: discardLogger()}
	require.NoError(t, m.submit(context.Background(), id))
	assert.Equal(t, int64(1), providerA.submitted.Load())
}

// TestSubmit_CircuitAlreadyOpen_GoesStraightToSecondCandidate is
// docs/plan/40 Task T3's scenario (c): the FIRST candidate's circuit is
// already open before any call this submit() makes — routing (Task T2)
// must skip it entirely, so vendorA.Submit is never even called.
func TestSubmit_CircuitAlreadyOpen_GoesStraightToSecondCandidate(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	holdTxID := uuid.New()
	settleTxID := uuid.New()

	req := sampleRequest(id, model.StatusHeld)
	req.HoldTxID = &holdTxID
	// Simulates the realistic path: Create() already ran ResolvePayoutRoute
	// and skipped vendorA (its circuit is open), so the row was routed to
	// vendorB from the start. submit() must simply honor req.Vendor as
	// given — vendorA.Submit is never even called.
	req.Vendor = "vendorB"

	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil).Times(2)
	repo.EXPECT().TransitionToSubmitted(gomock.Any(), id).Return(true, nil)
	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().TransitionToSettled(gomock.Any(), id, settleTxID).Return(true, nil)

	providerA := &stubPayoutProvider{name: "vendorA", submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
		t.Fatal("vendorA.Submit must never be called — its circuit is already open")
		return vendorgw.PayoutResult{}, nil
	}}
	providerB := &stubPayoutProvider{name: "vendorB", submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
		return vendorgw.PayoutResult{Status: vendorgw.PayoutSettled}, nil
	}}
	registry := vendorgw.NewRegistry()
	registry.AddPayout(providerA)
	registry.AddPayout(providerB)

	routing := multiVendorRouting{candidates: []model.RoutingCandidate{
		{Vendor: "vendorA", Gateway: "bca"},
		{Vendor: "vendorB", Gateway: "bri"},
	}}

	breaker := vendorgw.NewHealthTracker(1, time.Hour, nil)
	breaker.RecordFailure("vendorA") // opens the circuit before submit() runs at all

	poster := stubPoster{
		postFn: func(context.Context, ledgerclient.Command) error { return nil },
		getTxFn: func(context.Context, string, string) (ledgerclient.Transaction, error) {
			return ledgerclient.Transaction{ID: settleTxID}, nil
		},
	}

	// NOTE: submit() itself always calls provider.Submit against req.Vendor
	// (the row's CURRENT vendor) directly — it does not re-run routing for
	// the FIRST attempt. This test proves the realistic path: Create()
	// (which DOES call ResolvePayoutRoute) would have routed to vendorB in
	// the first place — see req.Vendor = "vendorB" above. Routing itself
	// skipping an open circuit is separately covered by
	// TestResolvePayoutRoute_BreakerOpen_SkipsToNextCandidate in
	// routing_test.go. Here we confirm submit() honors whatever vendor
	// routing already chose, i.e. vendorA is never touched.
	m := &Module{repo: repo, poster: poster, registry: registry, routing: routing, breaker: breaker, logger: discardLogger()}
	err := m.submit(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(0), providerA.submitted.Load())
	assert.Equal(t, int64(1), providerB.submitted.Load())
}

// assertAnErr is a tiny helper so scenario (b)'s stub can return a plain
// error without importing "errors" just for errors.New in this file.
type assertAnErr string

func (e assertAnErr) Error() string { return string(e) }

// TestMayFailover_TableDriven unit-tests the pure decision function in
// isolation (docs/plan/40 Task T3's own suggested signature).
func TestMayFailover_TableDriven(t *testing.T) {
	tests := []struct {
		name  string
		calls []model.PayoutVendorCall
		want  bool
	}{
		{"no calls yet", nil, true},
		{"one rejected call", []model.PayoutVendorCall{{Outcome: model.VendorCallRejected}}, true},
		{"two rejected calls", []model.PayoutVendorCall{{Outcome: model.VendorCallRejected}, {Outcome: model.VendorCallRejected}}, true},
		{"one accepted call", []model.PayoutVendorCall{{Outcome: model.VendorCallAccepted}}, false},
		{"one uncertain call", []model.PayoutVendorCall{{Outcome: model.VendorCallUncertain}}, false},
		{"rejected then accepted", []model.PayoutVendorCall{{Outcome: model.VendorCallRejected}, {Outcome: model.VendorCallAccepted}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mayFailover(tc.calls))
		})
	}
}
