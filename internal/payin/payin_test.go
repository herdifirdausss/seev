package payin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	fraudv1 "github.com/herdifirdausss/seev/gen/fraud/v1"
	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/internal/payin/repository"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
)

// fakeFraudGRPCClient is a minimal fraudv1.FraudServiceClient double for
// wrapping in a real *fraudcheck.Client — mirrors
// internal/ledger/transport/http_test.go's own double (docs/plan/37 Task T4).
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

// stubVerifier implements vendorgw.PayinVerifier directly against the
// interface contract — decouples payin's own tests from any concrete
// vendor's wire format (mockvendor has its own tests for that).
type stubVerifier struct {
	name  string
	event *vendorgw.PayinEvent
	err   error
}

func (s stubVerifier) Vendor() string { return s.name }
func (s stubVerifier) VerifyAndParse(http.Header, []byte) (*vendorgw.PayinEvent, error) {
	return s.event, s.err
}

func registryWith(v vendorgw.PayinVerifier) *vendorgw.Registry {
	r := vendorgw.NewRegistry()
	r.AddPayin(v)
	return r
}

type stubPoster struct {
	fn            func(ctx context.Context, cmd ledgerclient.Command) error
	getCurrencyFn func(ctx context.Context, userID uuid.UUID, pocketCode string) (string, error)
}

type stubRouting struct {
	vendor  string
	gateway string
	found   bool
}

func routeTo(vendor, gateway string) repository.RoutingRepository {
	return stubRouting{vendor: vendor, gateway: gateway, found: true}
}

func (s stubRouting) ResolveCandidates(context.Context, string, uuid.UUID, string, int64) ([]model.RoutingCandidate, error) {
	if !s.found {
		return nil, nil
	}
	return []model.RoutingCandidate{{Vendor: s.vendor, Gateway: s.gateway}}, nil
}
func (s stubRouting) ListRules(context.Context) ([]model.RoutingRule, error) { return nil, nil }
func (s stubRouting) CreateRule(context.Context, model.RoutingRule) error    { return nil }
func (s stubRouting) UpdateRule(context.Context, model.RoutingRule) error    { return nil }
func (s stubRouting) GetVendorGateway(_ context.Context, vendor string) (model.VendorGateway, bool, error) {
	if !s.found || vendor != s.vendor {
		return model.VendorGateway{}, false, nil
	}
	return model.VendorGateway{Vendor: vendor, Gateway: s.gateway}, true, nil
}
func (s stubRouting) ListVendorGateways(context.Context) ([]model.VendorGateway, error) {
	return nil, nil
}
func (s stubRouting) UpsertVendorGateway(context.Context, model.VendorGateway) error { return nil }

func (s stubPoster) Post(ctx context.Context, cmd ledgerclient.Command) error { return s.fn(ctx, cmd) }
func (s stubPoster) GetUserCurrency(ctx context.Context, userID uuid.UUID, pocketCode string) (string, error) {
	if s.getCurrencyFn != nil {
		return s.getCurrencyFn(ctx, userID, pocketCode)
	}
	return "IDR", nil
}

func sampleEvent() *vendorgw.PayinEvent {
	return &vendorgw.PayinEvent{
		Vendor:        "acme",
		VendorEventID: "evt-1",
		ExternalRef:   "ref-1",
		UserID:        uuid.New(),
		Amount:        decimal.NewFromInt(50_000),
		Currency:      "IDR",
		OccurredAt:    time.Now(),
	}
}

func TestHandleWebhook_UnknownVendor_ErrUnknownVendor(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected

	m := &Module{repo: repo, registry: vendorgw.NewRegistry(), routing: stubRouting{}}
	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	assert.ErrorIs(t, err, ErrUnknownVendor)
}

func TestHandleWebhook_BadSignature_NoSideEffect(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected

	verifier := stubVerifier{name: "acme", err: vendorgw.ErrInvalidSignature}
	m := &Module{repo: repo, registry: registryWith(verifier), routing: routeTo("acme", "bca")}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	assert.ErrorIs(t, err, vendorgw.ErrInvalidSignature)
}

func TestHandleWebhook_NonSettledEvent_AckedNoSideEffect(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected

	verifier := stubVerifier{name: "acme", event: nil, err: nil}
	m := &Module{repo: repo, registry: registryWith(verifier), routing: routeTo("acme", "bca")}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	assert.NoError(t, err)
}

func TestHandleWebhook_HappyPath_PostsAndMarksPosted(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	ev := sampleEvent()

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), "ref-1").Return(model.TopupIntent{}, false, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e model.WebhookEvent) (model.WebhookEvent, error) {
			e.Status = "received"
			return e, nil
		})
	repo.EXPECT().MarkPosted(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().MarkTopupIntentSettled(gomock.Any(), "ref-1", gomock.Any()).Return(false, nil)

	var gotCmd ledgerclient.Command
	poster := stubPoster{fn: func(_ context.Context, cmd ledgerclient.Command) error {
		gotCmd = cmd
		return nil
	}}

	verifier := stubVerifier{name: "acme", event: ev}
	m := &Module{repo: repo, poster: poster, registry: registryWith(verifier), routing: routeTo("acme", "bca"), logger: discardLogger()}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	require.NoError(t, err)

	assert.Equal(t, "money_in", gotCmd.Type)
	assert.Equal(t, "payin:acme:evt-1", gotCmd.IdempotencyKey)
	assert.Equal(t, "payin:acme", gotCmd.IdempotencyScope)
	assert.True(t, gotCmd.Amount.Equal(ev.Amount))
	assert.Equal(t, ev.UserID, gotCmd.UserID)
	assert.Equal(t, "bca", gotCmd.Metadata["gateway"])
	assert.Equal(t, "ref-1", gotCmd.Metadata["external_ref"])
}

func TestHandleWebhook_AlreadyPosted_PostNotCalledAgain(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), "ref-1").Return(model.TopupIntent{}, false, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e model.WebhookEvent) (model.WebhookEvent, error) {
			e.Status = "posted" // duplicate delivery of an already-settled event
			return e, nil
		})
	// MarkPosted must NOT be called — nothing to do.

	postCalls := 0
	poster := stubPoster{fn: func(_ context.Context, _ ledgerclient.Command) error {
		postCalls++
		return nil
	}}

	verifier := stubVerifier{name: "acme", event: sampleEvent()}
	m := &Module{repo: repo, poster: poster, registry: registryWith(verifier), routing: routeTo("acme", "bca")}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, 0, postCalls, "an already-posted event must never be posted again")
}

func TestHandleWebhook_InfraError_StaysReceived_NotMarkedFailed(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), "ref-1").Return(model.TopupIntent{}, false, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e model.WebhookEvent) (model.WebhookEvent, error) {
			e.Status = "received"
			return e, nil
		})
	// MarkFailed must NOT be called for an infra error — status stays
	// 'received' so a redelivery (or admin replay) tries again.

	poster := stubPoster{fn: func(_ context.Context, _ ledgerclient.Command) error {
		return errors.New("db connection reset")
	}}

	verifier := stubVerifier{name: "acme", event: sampleEvent()}
	m := &Module{repo: repo, poster: poster, registry: registryWith(verifier), routing: routeTo("acme", "bca"), logger: discardLogger()}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	require.Error(t, err)
	assert.False(t, IsBusinessFailure(err), "an infra error must not be classified as a business failure")
}

func TestHandleWebhook_BusinessFailure_MarkedFailed_NotRetryable(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), "ref-1").Return(model.TopupIntent{}, false, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e model.WebhookEvent) (model.WebhookEvent, error) {
			e.Status = "received"
			return e, nil
		})
	repo.EXPECT().MarkFailed(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	// Construct a business error via the root-facade re-export
	// (ledgererr.LedgerError, docs/plan/22 Task T2) — payin must never import
	// internal/ledger/apperror directly (boundary rule 1).
	bizErr := &ledgererr.LedgerError{Code: "ACCOUNT_SUSPENDED", Message: "account suspended"}
	poster := stubPoster{fn: func(_ context.Context, _ ledgerclient.Command) error {
		return bizErr
	}}

	verifier := stubVerifier{name: "acme", event: sampleEvent()}
	m := &Module{repo: repo, poster: poster, registry: registryWith(verifier), routing: routeTo("acme", "bca"), logger: discardLogger()}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	require.Error(t, err)
	assert.True(t, IsBusinessFailure(err), "a ledger business failure must be classified as such so the webhook receiver still acks 200")
}

func TestReplayEvent_PostedEvent_ErrAlreadyPosted(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()

	repo.EXPECT().Get(gomock.Any(), id).Return(model.WebhookEvent{ID: id, Vendor: "acme", Status: "posted"}, nil)

	m := &Module{repo: repo, routing: routeTo("acme", "bca")}
	err := m.ReplayEvent(context.Background(), id)
	assert.ErrorIs(t, err, ErrAlreadyPosted)
}

func TestReplayEvent_FailedEvent_RetriesPost(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()

	repo.EXPECT().Get(gomock.Any(), id).Return(model.WebhookEvent{
		ID: id, Vendor: "acme", VendorEventID: "evt-9", ExternalRef: "ref-9",
		UserID: uuid.New(), Amount: decimal.NewFromInt(1000), Currency: "IDR", Status: "failed",
	}, nil)
	repo.EXPECT().MarkPosted(gomock.Any(), id).Return(nil)
	repo.EXPECT().MarkTopupIntentSettled(gomock.Any(), "ref-9", id).Return(false, nil)

	postCalls := 0
	poster := stubPoster{fn: func(_ context.Context, _ ledgerclient.Command) error {
		postCalls++
		return nil
	}}

	m := &Module{repo: repo, poster: poster, routing: routeTo("acme", "bca"), logger: discardLogger()}
	err := m.ReplayEvent(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, 1, postCalls)
}

// TestHandleWebhook_FraudBlock_MarkedBlocked_PostNotCalled_Acked200 proves
// docs/plan/37 Task T4: a Block verdict marks the event 'blocked' (distinct
// from 'failed'), poster.Post is NEVER called (no double-posting risk), and
// the webhook receiver still acks 200 (business decision, non-retriable —
// the vendor already delivered the money).
func TestHandleWebhook_FraudBlock_MarkedBlocked_PostNotCalled_Acked200(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), "ref-1").Return(model.TopupIntent{}, false, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e model.WebhookEvent) (model.WebhookEvent, error) {
			e.Status = "received"
			return e, nil
		})
	repo.EXPECT().MarkBlocked(gomock.Any(), gomock.Any(), "over threshold").Return(nil)

	postCalls := 0
	poster := stubPoster{fn: func(_ context.Context, _ ledgerclient.Command) error {
		postCalls++
		return nil
	}}

	fraudClient := fraudcheck.New(&fakeFraudGRPCClient{
		response: &fraudv1.ScreenResponse{Block: true, Reason: "over threshold"},
	}, "payin")

	verifier := stubVerifier{name: "acme", event: sampleEvent()}
	m := &Module{repo: repo, poster: poster, registry: registryWith(verifier), routing: routeTo("acme", "bca"), logger: discardLogger(), fraudClient: fraudClient}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	require.Error(t, err)
	assert.True(t, IsBusinessFailure(err), "a fraud Block must be classified as a business failure so the webhook receiver still acks 200")
	assert.Equal(t, 0, postCalls, "a blocked deposit must never be posted to the ledger")
}

// TestHandleWebhook_FraudInfraError_FailsOpen_StillPosts proves the
// fail-open half of docs/plan/37 Task T4: a fraud-service/network error
// must NOT strand a real deposit — posting proceeds as if unscreened.
func TestHandleWebhook_FraudInfraError_FailsOpen_StillPosts(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), "ref-1").Return(model.TopupIntent{}, false, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e model.WebhookEvent) (model.WebhookEvent, error) {
			e.Status = "received"
			return e, nil
		})
	repo.EXPECT().MarkPosted(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().MarkTopupIntentSettled(gomock.Any(), "ref-1", gomock.Any()).Return(false, nil)

	postCalls := 0
	poster := stubPoster{fn: func(_ context.Context, _ ledgerclient.Command) error {
		postCalls++
		return nil
	}}

	fraudClient := fraudcheck.New(&fakeFraudGRPCClient{err: errors.New("fraud-service unreachable")}, "payin")

	verifier := stubVerifier{name: "acme", event: sampleEvent()}
	m := &Module{repo: repo, poster: poster, registry: registryWith(verifier), routing: routeTo("acme", "bca"), logger: discardLogger(), fraudClient: fraudClient}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, 1, postCalls, "a screening infra error must fail open, not strand the deposit")
}

// TestHandleWebhook_FraudDependencyUnavailable_FailsClosed_NotBusinessFailure
// proves docs/plan/45 Task T3/K4: fraud-service reachable but explicitly
// signaling its velocity dependency is down must fail CLOSED — poster.Post
// is NEVER called, unlike the fail-open infra-error case above — and,
// critically, this must NOT be classified as a businessError (unlike the
// Block case above): the identical webhook redelivery should succeed once
// Redis recovers, so the webhook receiver must respond in a way that makes
// the vendor retry (503), not ack 200 as a settled business decision.
func TestHandleWebhook_FraudDependencyUnavailable_FailsClosed_NotBusinessFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), "ref-1").Return(model.TopupIntent{}, false, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e model.WebhookEvent) (model.WebhookEvent, error) {
			e.Status = "received"
			return e, nil
		})
	// No MarkBlocked/MarkPosted call expected — the event stays 'received'
	// so the vendor's own redelivery re-screens it later.

	postCalls := 0
	poster := stubPoster{fn: func(_ context.Context, _ ledgerclient.Command) error {
		postCalls++
		return nil
	}}

	fraudClient := fraudcheck.New(&fakeFraudGRPCClient{
		err: status.Error(codes.FailedPrecondition, "DEPENDENCY_UNAVAILABLE"),
	}, "payin")

	verifier := stubVerifier{name: "acme", event: sampleEvent()}
	m := &Module{repo: repo, poster: poster, registry: registryWith(verifier), routing: routeTo("acme", "bca"), logger: discardLogger(), fraudClient: fraudClient}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrScreeningDependencyUnavailable)
	assert.False(t, IsBusinessFailure(err), "must NOT be a business failure — the identical redelivery should succeed once Redis recovers")
	assert.Equal(t, 0, postCalls, "no deposit may post while the dependency is unavailable")
}

// TestReplayEvent_BlockedEvent_ReScreens proves admin replay of a
// previously-blocked event RE-SCREENS (deliberate, docs/plan/37 Task T4) —
// it does not just retry the old verdict. Here the underlying condition has
// cleared (fraud now allows it), so the replay posts successfully.
func TestReplayEvent_BlockedEvent_ReScreens(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()

	repo.EXPECT().Get(gomock.Any(), id).Return(model.WebhookEvent{
		ID: id, Vendor: "acme", VendorEventID: "evt-9", ExternalRef: "ref-9",
		UserID: uuid.New(), Amount: decimal.NewFromInt(1000), Currency: "IDR", Status: "blocked",
	}, nil)
	repo.EXPECT().MarkPosted(gomock.Any(), id).Return(nil)
	repo.EXPECT().MarkTopupIntentSettled(gomock.Any(), "ref-9", id).Return(false, nil)

	fraudClient := fraudcheck.New(&fakeFraudGRPCClient{
		response: &fraudv1.ScreenResponse{Block: false},
	}, "payin")

	poster := stubPoster{fn: func(_ context.Context, _ ledgerclient.Command) error { return nil }}

	m := &Module{repo: repo, poster: poster, routing: routeTo("acme", "bca"), logger: discardLogger(), fraudClient: fraudClient}
	err := m.ReplayEvent(context.Background(), id)
	require.NoError(t, err, "replay must re-screen and, with the block now cleared, post successfully")
}
