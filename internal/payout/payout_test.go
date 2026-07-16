package payout

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"

	fraudv1 "github.com/herdifirdausss/seev/gen/fraud/v1"
	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/payout/repository"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
)

// fakeFraudGRPCClient is a minimal fraudv1.FraudServiceClient double for
// wrapping in a real *fraudcheck.Client — mirrors the same pattern used in
// internal/ledger/transport/http_test.go and internal/payin/payin_test.go
// (docs/plan/37 Task T5).
type fakeFraudGRPCClient struct {
	response *fraudv1.ScreenResponse
	err      error
}

func (f *fakeFraudGRPCClient) Screen(_ context.Context, _ *fraudv1.ScreenRequest, _ ...grpc.CallOption) (*fraudv1.ScreenResponse, error) {
	return f.response, f.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stubPoster implements the payout.Poster interface directly, decoupling
// these tests from any concrete ledger.Module wiring (mirrors
// internal/payin's own stubPoster, docs/plan/22 Task T2).
type stubPoster struct {
	postFn         func(ctx context.Context, cmd ledgerclient.Command) error
	getTxFn        func(ctx context.Context, key, scope string) (ledgerclient.Transaction, error)
	getCurrencyFn  func(ctx context.Context, userID uuid.UUID, pocketCode string) (string, error)
	resolveFeeFn   func(txType, gateway, currency string, amount decimal.Decimal) (decimal.Decimal, string, bool)
	consumeQuoteFn func(quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, ref string) (decimal.Decimal, string, error)
}

func (s stubPoster) Post(ctx context.Context, cmd ledgerclient.Command) error {
	return s.postFn(ctx, cmd)
}
func (s stubPoster) GetTransactionByIdempotencyKey(ctx context.Context, key, scope string) (ledgerclient.Transaction, error) {
	return s.getTxFn(ctx, key, scope)
}
func (s stubPoster) GetUserCurrency(ctx context.Context, userID uuid.UUID, pocketCode string) (string, error) {
	if s.getCurrencyFn != nil {
		return s.getCurrencyFn(ctx, userID, pocketCode)
	}
	return "IDR", nil
}
func (s stubPoster) ResolveFee(_ context.Context, _ uuid.UUID, txType, gateway, currency string, amount decimal.Decimal) (decimal.Decimal, string, bool, error) {
	if s.resolveFeeFn != nil {
		fee, feeGateway, ok := s.resolveFeeFn(txType, gateway, currency, amount)
		return fee, feeGateway, ok, nil
	}
	return decimal.Zero, "", false, nil
}
func (s stubPoster) ConsumeFeeQuote(_ context.Context, quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, ref string) (decimal.Decimal, string, error) {
	if s.consumeQuoteFn != nil {
		return s.consumeQuoteFn(quoteID, userID, txType, currency, amount, ref)
	}
	return decimal.Zero, "", nil
}

// stubPayoutProvider implements vendorgw.PayoutProvider directly against the
// interface contract (mirrors internal/payin's stubVerifier pattern) —
// decouples these tests from mockvendor's own concrete behavior.
type stubPayoutProvider struct {
	name      string
	submitFn  func(ctx context.Context, idempotencyKey string, amount decimal.Decimal, currency string, destination json.RawMessage) (vendorgw.PayoutResult, error)
	queryFn   func(ctx context.Context, idempotencyKey string) (vendorgw.PayoutResult, error)
	submitted int
	queried   int
}

func (s *stubPayoutProvider) Vendor() string { return s.name }
func (s *stubPayoutProvider) Submit(ctx context.Context, idempotencyKey string, amount decimal.Decimal, currency string, destination json.RawMessage) (vendorgw.PayoutResult, error) {
	s.submitted++
	return s.submitFn(ctx, idempotencyKey, amount, currency, destination)
}
func (s *stubPayoutProvider) Query(ctx context.Context, idempotencyKey string) (vendorgw.PayoutResult, error) {
	s.queried++
	return s.queryFn(ctx, idempotencyKey)
}

func registryWith(v vendorgw.PayoutProvider) *vendorgw.Registry {
	r := vendorgw.NewRegistry()
	r.AddPayout(v)
	return r
}

type stubRouting struct {
	vendor, gateway string
	found           bool
}

func routeTo(vendor, gateway string) repository.RoutingRepository {
	return stubRouting{vendor: vendor, gateway: gateway, found: true}
}
func (s stubRouting) Resolve(context.Context, string, uuid.UUID, string, int64) (model.RoutingRule, string, bool, error) {
	return model.RoutingRule{Vendor: s.vendor}, s.gateway, s.found, nil
}
func (stubRouting) ListRules(context.Context) ([]model.RoutingRule, error) { return nil, nil }
func (stubRouting) CreateRule(context.Context, model.RoutingRule) error    { return nil }
func (stubRouting) UpdateRule(context.Context, model.RoutingRule) error    { return nil }
func (s stubRouting) GetVendorGateway(_ context.Context, vendor string) (model.VendorGateway, bool, error) {
	if !s.found || vendor != s.vendor {
		return model.VendorGateway{}, false, nil
	}
	return model.VendorGateway{Vendor: vendor, Gateway: s.gateway}, true, nil
}
func (stubRouting) UpsertVendorGateway(context.Context, model.VendorGateway) error { return nil }

func newTestModule(repo repository.Repository, poster Poster, registry *vendorgw.Registry) *Module {
	return &Module{
		repo: repo, poster: poster, registry: registry,
		routing: routeTo("mockvendor", "bca"),
		logger:  discardLogger(),
	}
}

func sampleRequest(id uuid.UUID, status string) model.PayoutRequest {
	return model.PayoutRequest{
		ID: id, UserID: uuid.New(), Amount: decimal.NewFromInt(100_000), Currency: "IDR",
		Vendor: "mockvendor", Destination: []byte(`{}`), Status: status, CreatedBy: "test",
	}
}

func TestCreate_NoRoute(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected

	m := &Module{repo: repo, poster: stubPoster{}, registry: vendorgw.NewRegistry(), routing: stubRouting{}, logger: discardLogger()}
	_, err := m.Create(context.Background(), uuid.New(), decimal.NewFromInt(1000), []byte(`{}`), "test", "")
	assert.ErrorIs(t, err, ErrNoRoute)
}

func TestCreate_RoutedVendorNotRegistered_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected

	m := &Module{repo: repo, poster: stubPoster{}, registry: vendorgw.NewRegistry(), routing: routeTo("mockvendor", "bca"), logger: discardLogger()}
	_, err := m.Create(context.Background(), uuid.New(), decimal.NewFromInt(1000), []byte(`{}`), "test", "")
	require.Error(t, err)
}

// TestCreate_HappyPath_InstantSettle proves Create drives created -> held ->
// submitted -> settled in one call when the vendor settles synchronously
// (docs/plan/23 Task T3's "instant-settle" mode).
func TestCreate_HappyPath_InstantSettle(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	holdTxID := uuid.New()
	settleTxID := uuid.New()

	heldReq := sampleRequest(uuid.Nil, model.StatusHeld)
	heldReq.HoldTxID = &holdTxID

	repo.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().TransitionToHeld(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
	repo.EXPECT().Get(gomock.Any(), gomock.Any()).Return(heldReq, nil).Times(2)
	repo.EXPECT().TransitionToSubmitted(gomock.Any(), gomock.Any()).Return(true, nil)
	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().TransitionToSettled(gomock.Any(), gomock.Any(), settleTxID).Return(true, nil)

	provider := &stubPayoutProvider{
		name: "mockvendor",
		submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
			return vendorgw.PayoutResult{Status: vendorgw.PayoutSettled}, nil
		},
	}

	poster := stubPoster{
		postFn: func(_ context.Context, _ ledgerclient.Command) error { return nil },
		getTxFn: func(_ context.Context, key, _ string) (ledgerclient.Transaction, error) {
			if key[len(key)-4:] == "hold" {
				return ledgerclient.Transaction{ID: holdTxID}, nil
			}
			return ledgerclient.Transaction{ID: settleTxID}, nil
		},
	}

	m := newTestModule(repo, poster, registryWith(provider))
	id, err := m.Create(context.Background(), userID, decimal.NewFromInt(100_000), []byte(`{}`), "test", "")
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
	assert.Equal(t, 1, provider.submitted)
}

// TestCreate_FraudBlock_NoRowInserted_NoHold proves docs/plan/37 Task T5: a
// Block verdict rejects Create BEFORE any payout_requests row is inserted
// and BEFORE any hold is posted — repo.Insert is never called at all, so the
// only audit trail for a blocked attempt lives in fraud-service's own
// screening_events, matching the ledger/payin precedent from T3/T4.
func TestCreate_FraudBlock_NoRowInserted_NoHold(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected at all

	fraudClient := fraudcheck.New(&fakeFraudGRPCClient{
		response: &fraudv1.ScreenResponse{Block: true, Reason: "over threshold"},
	}, "payout")

	m := &Module{repo: repo, poster: stubPoster{}, registry: vendorgw.NewRegistry(), routing: routeTo("mockvendor", "bca"), logger: discardLogger(), fraudClient: fraudClient}
	id, err := m.Create(context.Background(), uuid.New(), decimal.NewFromInt(1_000_000), []byte(`{}`), "test", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrScreeningBlocked)
	assert.Equal(t, uuid.Nil, id)
}

// TestCreate_FraudInfraError_FailsOpen_StillCreates proves the fail-open
// half of docs/plan/37 Task T5: a fraud-service/network error must not
// block a legitimate payout — Create proceeds exactly as the unscreened
// happy path does.
func TestCreate_FraudInfraError_FailsOpen_StillCreates(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	holdTxID := uuid.New()
	settleTxID := uuid.New()

	heldReq := sampleRequest(uuid.Nil, model.StatusHeld)
	heldReq.HoldTxID = &holdTxID

	repo.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().TransitionToHeld(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
	repo.EXPECT().Get(gomock.Any(), gomock.Any()).Return(heldReq, nil).Times(2)
	repo.EXPECT().TransitionToSubmitted(gomock.Any(), gomock.Any()).Return(true, nil)
	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().TransitionToSettled(gomock.Any(), gomock.Any(), settleTxID).Return(true, nil)

	provider := &stubPayoutProvider{
		name: "mockvendor",
		submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
			return vendorgw.PayoutResult{Status: vendorgw.PayoutSettled}, nil
		},
	}
	poster := stubPoster{
		postFn: func(_ context.Context, _ ledgerclient.Command) error { return nil },
		getTxFn: func(_ context.Context, key, _ string) (ledgerclient.Transaction, error) {
			if key[len(key)-4:] == "hold" {
				return ledgerclient.Transaction{ID: holdTxID}, nil
			}
			return ledgerclient.Transaction{ID: settleTxID}, nil
		},
	}

	fraudClient := fraudcheck.New(&fakeFraudGRPCClient{err: errors.New("fraud-service unreachable")}, "payout")

	m := &Module{repo: repo, poster: poster, registry: registryWith(provider), routing: routeTo("mockvendor", "bca"), logger: discardLogger(), fraudClient: fraudClient}
	id, err := m.Create(context.Background(), userID, decimal.NewFromInt(100_000), []byte(`{}`), "test", "")
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
}

// TestSettle_NeverScreened_EvenWithBlockingFraudClient proves docs/plan/37
// Task T5's "gotcha #8" requirement: settle/cancel are NEVER screened, even
// when a fraudClient is configured and would block everything — money is
// already held, so blocking settle would strand funds. The fraud client
// here would reject any Screen call; settle() succeeding proves it was
// never called.
func TestSettle_NeverScreened_EvenWithBlockingFraudClient(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	holdTxID := uuid.New()
	settleTxID := uuid.New()

	req := sampleRequest(id, model.StatusSubmitted)
	req.HoldTxID = &holdTxID

	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil)
	repo.EXPECT().TransitionToSettled(gomock.Any(), id, settleTxID).Return(true, nil)

	poster := stubPoster{
		postFn: func(_ context.Context, _ ledgerclient.Command) error { return nil },
		getTxFn: func(_ context.Context, _, _ string) (ledgerclient.Transaction, error) {
			return ledgerclient.Transaction{ID: settleTxID}, nil
		},
	}

	fraudClient := fraudcheck.New(&fakeFraudGRPCClient{
		response: &fraudv1.ScreenResponse{Block: true, Reason: "would block everything"},
	}, "payout")

	m := &Module{repo: repo, poster: poster, routing: routeTo("mockvendor", "bca"), logger: discardLogger(), fraudClient: fraudClient}
	err := m.settle(context.Background(), id, "bca")
	require.NoError(t, err, "settle must never screen — a blocking fraud client must have no effect on it")
}

// TestSubmit_VendorPending_TransitionsToVendorPending proves the async path
// (docs/plan/23 Task T3's "async" mode) leaves the request in
// vendor_pending for the resume job to poll later, rather than forcing a
// terminal state immediately.
func TestSubmit_VendorPending_TransitionsToVendorPending(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()

	repo.EXPECT().Get(gomock.Any(), id).Return(sampleRequest(id, model.StatusHeld), nil)
	repo.EXPECT().TransitionToSubmitted(gomock.Any(), id).Return(true, nil)
	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().TransitionToVendorPending(gomock.Any(), id, "vendor-ref-1").Return(true, nil)

	provider := &stubPayoutProvider{
		name: "mockvendor",
		submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
			return vendorgw.PayoutResult{Status: vendorgw.PayoutPending, VendorRef: "vendor-ref-1"}, nil
		},
	}

	m := newTestModule(repo, stubPoster{}, registryWith(provider))
	err := m.submit(context.Background(), id)
	require.NoError(t, err)
}

// TestSubmit_VendorFailed_TransitionsToCancelled proves a synchronous vendor
// failure drives cancel() (which returns the held money) instead of leaving
// the request stuck.
func TestSubmit_VendorFailed_CancelsAndReturnsHold(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	holdTxID := uuid.New()
	cancelTxID := uuid.New()

	req := sampleRequest(id, model.StatusHeld)
	req.HoldTxID = &holdTxID

	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil).Times(2)
	repo.EXPECT().TransitionToSubmitted(gomock.Any(), id).Return(true, nil)
	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().TransitionToCancelled(gomock.Any(), id, cancelTxID).Return(true, nil)
	repo.EXPECT().SetError(gomock.Any(), id, "vendor declined").Return(nil)

	provider := &stubPayoutProvider{
		name: "mockvendor",
		submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
			return vendorgw.PayoutResult{Status: vendorgw.PayoutFailed, Reason: "vendor declined"}, nil
		},
	}

	poster := stubPoster{
		postFn: func(_ context.Context, _ ledgerclient.Command) error { return nil },
		getTxFn: func(_ context.Context, _, _ string) (ledgerclient.Transaction, error) {
			return ledgerclient.Transaction{ID: cancelTxID}, nil
		},
	}

	m := newTestModule(repo, poster, registryWith(provider))
	err := m.submit(context.Background(), id)
	require.NoError(t, err)
}

// TestSettle_LostRace_Reconciles proves the K3-guard-reliance philosophy
// (docs/plan/23 Task T4): losing ledgererr.ErrAlreadyClosed is NOT propagated
// as an error — it's reconciled, since the request already reached a
// terminal state via a different concurrent path.
func TestSettle_LostRace_Reconciles(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	holdTxID := uuid.New()

	req := sampleRequest(id, model.StatusCancelled)
	req.HoldTxID = &holdTxID

	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil).Times(2)
	repo.EXPECT().SetError(gomock.Any(), id, gomock.Any()).Return(nil)

	poster := stubPoster{
		postFn: func(_ context.Context, _ ledgerclient.Command) error { return ledgererr.ErrAlreadyClosed },
	}

	m := newTestModule(repo, poster, vendorgw.NewRegistry())
	err := m.settle(context.Background(), id, "bca")
	assert.NoError(t, err, "a lost K3 race must be reconciled, never surfaced as a caller-visible error")
}

// TestCancel_LostRace_Reconciles is settle's mirror image: a late cancel
// attempt after the request already settled via a different path.
func TestCancel_LostRace_Reconciles(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	holdTxID := uuid.New()

	req := sampleRequest(id, model.StatusSettled)
	req.HoldTxID = &holdTxID

	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil).Times(2)
	repo.EXPECT().SetError(gomock.Any(), id, gomock.Any()).Return(nil)

	poster := stubPoster{
		postFn: func(_ context.Context, _ ledgerclient.Command) error { return ledgererr.ErrAlreadyClosed },
	}

	m := newTestModule(repo, poster, vendorgw.NewRegistry())
	err := m.cancel(context.Background(), id, "bca", "late cancel")
	assert.NoError(t, err, "a lost K3 race must be reconciled, never surfaced as a caller-visible error")
}

// TestResumeStuck_RetriesSubmittedAndPollsVendorPending proves the resume
// job's two-pronged behavior (docs/plan/23 Task T3 step 3): a stuck
// 'submitted' request gets Submit retried; a stuck 'vendor_pending' request
// gets Query'd and routed to a terminal state.
func TestResumeStuck_RetriesSubmittedAndPollsVendorPending(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	stuckSubmittedID := uuid.New()
	stuckPendingID := uuid.New()
	settleTxID := uuid.New()
	holdTxID := uuid.New()

	submittedReq := sampleRequest(stuckSubmittedID, model.StatusSubmitted)
	submittedReq.HoldTxID = &holdTxID
	pendingReq := sampleRequest(stuckPendingID, model.StatusVendorPending)
	pendingReq.HoldTxID = &holdTxID

	repo.EXPECT().ListStuck(gomock.Any(), model.StatusCreated, gomock.Any(), gomock.Any()).Return(nil, nil)
	repo.EXPECT().ListStuck(gomock.Any(), model.StatusHeld, gomock.Any(), gomock.Any()).Return(nil, nil)
	repo.EXPECT().ListStuck(gomock.Any(), model.StatusSubmitted, gomock.Any(), gomock.Any()).
		Return([]model.PayoutRequest{submittedReq}, nil)
	repo.EXPECT().ListStuck(gomock.Any(), model.StatusVendorPending, gomock.Any(), gomock.Any()).
		Return([]model.PayoutRequest{pendingReq}, nil)

	// stuckSubmittedID: submit() retried (Get once for submit, once more
	// inside the settle() it triggers since the vendor now reports Settled).
	repo.EXPECT().Get(gomock.Any(), stuckSubmittedID).Return(submittedReq, nil).Times(2)
	repo.EXPECT().TransitionToSubmitted(gomock.Any(), stuckSubmittedID).Return(true, nil)

	// stuckPendingID: polled directly via the ListStuck row (no repo.Get in
	// pollVendorPending itself), then settle() does its own single Get.
	repo.EXPECT().Get(gomock.Any(), stuckPendingID).Return(pendingReq, nil)
	repo.EXPECT().TransitionToSettled(gomock.Any(), stuckSubmittedID, settleTxID).Return(true, nil)
	repo.EXPECT().TransitionToSettled(gomock.Any(), stuckPendingID, settleTxID).Return(true, nil)

	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	provider := &stubPayoutProvider{
		name: "mockvendor",
		submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
			return vendorgw.PayoutResult{Status: vendorgw.PayoutSettled}, nil
		},
		queryFn: func(context.Context, string) (vendorgw.PayoutResult, error) {
			return vendorgw.PayoutResult{Status: vendorgw.PayoutSettled}, nil
		},
	}

	poster := stubPoster{
		postFn: func(_ context.Context, _ ledgerclient.Command) error { return nil },
		getTxFn: func(_ context.Context, _, _ string) (ledgerclient.Transaction, error) {
			return ledgerclient.Transaction{ID: settleTxID}, nil
		},
	}

	m := newTestModule(repo, poster, registryWith(provider))
	resumed, failed, err := m.ResumeStuck(context.Background(), 0)
	require.NoError(t, err)
	assert.Equal(t, 2, resumed)
	assert.Equal(t, 0, failed)
	assert.Equal(t, 1, provider.submitted)
	assert.Equal(t, 1, provider.queried)
}

// TestResumeStuck_VendorStillPending_NotCountedAsFailure proves a genuinely
// still-pending vendor response is not an error — the request simply waits
// for the next resume pass.
func TestResumeStuck_VendorStillPending_NoTransitionCalled(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	holdTxID := uuid.New()

	req := sampleRequest(id, model.StatusVendorPending)
	req.HoldTxID = &holdTxID

	repo.EXPECT().ListStuck(gomock.Any(), model.StatusCreated, gomock.Any(), gomock.Any()).Return(nil, nil)
	repo.EXPECT().ListStuck(gomock.Any(), model.StatusHeld, gomock.Any(), gomock.Any()).Return(nil, nil)
	repo.EXPECT().ListStuck(gomock.Any(), model.StatusSubmitted, gomock.Any(), gomock.Any()).Return(nil, nil)
	repo.EXPECT().ListStuck(gomock.Any(), model.StatusVendorPending, gomock.Any(), gomock.Any()).Return([]model.PayoutRequest{req}, nil)
	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(nil)
	// No TransitionToSettled/Cancelled call expected — still pending.

	provider := &stubPayoutProvider{
		name: "mockvendor",
		queryFn: func(context.Context, string) (vendorgw.PayoutResult, error) {
			return vendorgw.PayoutResult{Status: vendorgw.PayoutPending}, nil
		},
	}

	m := newTestModule(repo, stubPoster{}, registryWith(provider))
	resumed, failed, err := m.ResumeStuck(context.Background(), 0)
	require.NoError(t, err)
	assert.Equal(t, 1, resumed)
	assert.Equal(t, 0, failed)
}

// TestResumeStuck_CreatedStuck_RetriesHoldThenSubmit proves the gap found
// while designing docs/plan/23 Task T6's chaos scenario: a crash right
// after 'created' (before hold() ever ran) must not orphan the request
// forever — the resume job retries hold() (idempotent by deterministic
// ledger idempotency key) and then falls through into submit() in the same
// pass, exactly mirroring what Create() itself does inline.
func TestResumeStuck_CreatedStuck_RetriesHoldThenSubmit(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	holdTxID := uuid.New()
	createdReq := sampleRequest(id, model.StatusCreated)

	heldReq := createdReq
	heldReq.Status = model.StatusHeld
	heldReq.HoldTxID = &holdTxID

	repo.EXPECT().ListStuck(gomock.Any(), model.StatusCreated, gomock.Any(), gomock.Any()).
		Return([]model.PayoutRequest{createdReq}, nil)
	repo.EXPECT().ListStuck(gomock.Any(), model.StatusHeld, gomock.Any(), gomock.Any()).Return(nil, nil)
	repo.EXPECT().ListStuck(gomock.Any(), model.StatusSubmitted, gomock.Any(), gomock.Any()).Return(nil, nil)
	repo.EXPECT().ListStuck(gomock.Any(), model.StatusVendorPending, gomock.Any(), gomock.Any()).Return(nil, nil)

	repo.EXPECT().TransitionToHeld(gomock.Any(), id, gomock.Any()).Return(true, nil)
	repo.EXPECT().Get(gomock.Any(), id).Return(heldReq, nil)
	repo.EXPECT().TransitionToSubmitted(gomock.Any(), id).Return(true, nil)
	repo.EXPECT().InsertVendorCall(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().TransitionToVendorPending(gomock.Any(), id, "vref-resume").Return(true, nil)

	provider := &stubPayoutProvider{
		name: "mockvendor",
		submitFn: func(context.Context, string, decimal.Decimal, string, json.RawMessage) (vendorgw.PayoutResult, error) {
			return vendorgw.PayoutResult{Status: vendorgw.PayoutPending, VendorRef: "vref-resume"}, nil
		},
	}
	poster := stubPoster{
		postFn: func(context.Context, ledgerclient.Command) error { return nil },
		getTxFn: func(context.Context, string, string) (ledgerclient.Transaction, error) {
			return ledgerclient.Transaction{ID: holdTxID}, nil
		},
	}

	m := newTestModule(repo, poster, registryWith(provider))
	resumed, failed, err := m.ResumeStuck(context.Background(), 0)
	require.NoError(t, err)
	assert.Equal(t, 1, resumed)
	assert.Equal(t, 0, failed)
}

func TestResumeStuck_ListStuckError_Propagates(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)

	repo.EXPECT().ListStuck(gomock.Any(), model.StatusCreated, gomock.Any(), gomock.Any()).
		Return(nil, errors.New("db down"))

	m := newTestModule(repo, stubPoster{}, vendorgw.NewRegistry())
	_, _, err := m.ResumeStuck(context.Background(), 0)
	require.Error(t, err)
}
