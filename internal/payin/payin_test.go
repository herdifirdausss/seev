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
)

type fakeFraudGRPCClient struct {
	response *fraudv1.ScreenResponse
	err      error
}

func (f *fakeFraudGRPCClient) Screen(_ context.Context, _ *fraudv1.ScreenRequest, _ ...grpc.CallOption) (*fraudv1.ScreenResponse, error) {
	return f.response, f.err
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type stubVerifier struct{ name string }

func (s stubVerifier) Vendor() string { return s.name }
func (stubVerifier) SupportsCurrency(operation, currency string) bool {
	return operation == "topup" && (currency == "IDR" || currency == "USD")
}
func (s stubVerifier) VerifyAndParse(http.Header, []byte) (*vendorgw.PayinEvent, error) {
	return nil, errors.New("raw payin callback must be handled by VendorService")
}

func registryWith(v vendorgw.PayinVerifier) *vendorgw.Registry {
	r := vendorgw.NewRegistry()
	r.AddPayin(v)
	return r
}

type stubPoster struct {
	fn            func(context.Context, ledgerclient.Command) error
	getCurrencyFn func(context.Context, uuid.UUID, string) (string, error)
}

func (s stubPoster) Post(ctx context.Context, cmd ledgerclient.Command) error { return s.fn(ctx, cmd) }
func (s stubPoster) GetUserCurrency(ctx context.Context, userID uuid.UUID, pocketCode string) (string, error) {
	if s.getCurrencyFn != nil {
		return s.getCurrencyFn(ctx, userID, pocketCode)
	}
	return "IDR", nil
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

func sampleCallback() (string, string, string, string, string, string) {
	return "acme", "evt-1", "TOP-1", "50000", "IDR", "inbox-1"
}

func TestHandleVendorCallback_PostsForOwnedIntent(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	vendor, eventID, reference, amount, currency, inboxID := sampleCallback()
	userID := uuid.New()

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), reference).Return(model.TopupIntent{
		Reference: reference, UserID: userID, Vendor: vendor, Amount: decimal.RequireFromString(amount), Currency: currency,
		Status: model.TopupStatusPending, ExpiresAt: time.Now().Add(time.Hour),
	}, true, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, event model.WebhookEvent) (model.WebhookEvent, error) {
		assert.Equal(t, userID, event.UserID)
		event.Status = "received"
		return event, nil
	})
	repo.EXPECT().MarkPosted(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().MarkTopupIntentSettled(gomock.Any(), reference, gomock.Any()).Return(true, nil)

	var posted ledgerclient.Command
	m := &Module{repo: repo, poster: stubPoster{fn: func(_ context.Context, cmd ledgerclient.Command) error { posted = cmd; return nil }}, routing: routeTo(vendor, "bca"), logger: discardLogger()}
	outcome, err := m.HandleVendorCallback(context.Background(), vendor, eventID, reference, amount, currency, "settled", "2026-07-13T00:00:00Z", inboxID, "req-1", "")
	require.NoError(t, err)
	assert.Equal(t, VendorCallbackFinalized, outcome)
	assert.Equal(t, userID, posted.UserID)
	assert.Equal(t, "payin:acme:evt-1", posted.IdempotencyKey)
}

func TestHandleVendorCallback_UnmatchedIntentDoesNotPost(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	vendor, eventID, reference, amount, currency, inboxID := sampleCallback()
	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), reference).Return(model.TopupIntent{}, false, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).Return(model.WebhookEvent{ID: uuid.New(), Status: "received"}, nil)
	repo.EXPECT().MarkFailed(gomock.Any(), gomock.Any(), "no matching payin intent").Return(nil)

	posts := 0
	m := &Module{repo: repo, poster: stubPoster{fn: func(context.Context, ledgerclient.Command) error { posts++; return nil }}, routing: routeTo(vendor, "bca"), logger: discardLogger()}
	outcome, err := m.HandleVendorCallback(context.Background(), vendor, eventID, reference, amount, currency, "settled", "", inboxID, "req-1", "")
	require.NoError(t, err)
	assert.Equal(t, VendorCallbackRecordedUnmatched, outcome)
	assert.Zero(t, posts)
}

func TestHandleVendorCallback_InvalidAmountFailsClosed(t *testing.T) {
	m := &Module{}
	_, err := m.HandleVendorCallback(context.Background(), "acme", "evt-1", "TOP-1", "not-an-amount", "IDR", "settled", "", "inbox-1", "req-1", "")
	assert.Error(t, err)
}

func TestReplayEvent_PostedEvent_ErrAlreadyPosted(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().Get(gomock.Any(), id).Return(model.WebhookEvent{ID: id, Status: "posted"}, nil)
	m := &Module{repo: repo, routing: routeTo("acme", "bca")}
	assert.ErrorIs(t, m.ReplayEvent(context.Background(), id), ErrAlreadyPosted)
}

func TestReplayEvent_FailedEvent_RetriesPost(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().Get(gomock.Any(), id).Return(model.WebhookEvent{ID: id, Vendor: "acme", VendorEventID: "evt-9", ExternalRef: "ref-9", UserID: uuid.New(), Amount: decimal.NewFromInt(1000), Currency: "IDR", Status: "failed"}, nil)
	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), "ref-9").Return(model.TopupIntent{}, false, nil)
	repo.EXPECT().MarkPosted(gomock.Any(), id).Return(nil)
	repo.EXPECT().MarkTopupIntentSettled(gomock.Any(), "ref-9", id).Return(false, nil)
	m := &Module{repo: repo, poster: stubPoster{fn: func(context.Context, ledgerclient.Command) error { return nil }}, routing: routeTo("acme", "bca"), logger: discardLogger()}
	require.NoError(t, m.ReplayEvent(context.Background(), id))
}

func TestHandleVendorCallback_FraudDependencyUnavailableIsRetryable(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	vendor, eventID, reference, amount, currency, inboxID := sampleCallback()
	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), reference).Return(model.TopupIntent{Reference: reference, UserID: uuid.New(), Vendor: vendor, Amount: decimal.RequireFromString(amount), Currency: currency, Status: model.TopupStatusPending, ExpiresAt: time.Now().Add(time.Hour)}, true, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).Return(model.WebhookEvent{ID: uuid.New(), Status: "received"}, nil)
	fraud := fraudcheck.New(&fakeFraudGRPCClient{err: status.Error(codes.FailedPrecondition, "DEPENDENCY_UNAVAILABLE")}, "payin")
	m := &Module{repo: repo, poster: stubPoster{fn: func(context.Context, ledgerclient.Command) error { t.Fatal("must not post"); return nil }}, routing: routeTo(vendor, "bca"), logger: discardLogger(), fraudClient: fraud}
	_, err := m.HandleVendorCallback(context.Background(), vendor, eventID, reference, amount, currency, "settled", "", inboxID, "req-1", "")
	assert.Error(t, err)
}
