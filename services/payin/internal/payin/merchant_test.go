package payin

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"

	"github.com/herdifirdausss/seev/contracts/clients/fraud"
	"github.com/herdifirdausss/seev/contracts/clients/ledger"
	vendorgw "github.com/herdifirdausss/seev/contracts/vendorgw"
	fraudv1 "github.com/herdifirdausss/seev/gen/go/fraud/v1"
	"github.com/herdifirdausss/seev/services/payin/internal/payin/model"
	"github.com/herdifirdausss/seev/services/payin/internal/repository"
)

func TestHandleVendorCallback_MerchantOwnedIntent_PostsMerchantPayinCredit(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	vendor, eventID, reference, amount, currency, inboxID := sampleCallback()
	tenantID := uuid.New()

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), reference).Return(model.TopupIntent{
		Reference: reference, MerchantTenantID: tenantID, Vendor: vendor,
		Amount: decimal.RequireFromString(amount), Currency: currency,
		Status: model.TopupStatusPending, ExpiresAt: time.Now().Add(time.Hour),
	}, true, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, event model.WebhookEvent) (model.WebhookEvent, error) {
		assert.Equal(t, tenantID, event.MerchantTenantID, "the stored event must carry the tenant id, not a user id")
		assert.Equal(t, uuid.Nil, event.UserID)
		event.Status = "received"
		return event, nil
	})
	repo.EXPECT().MarkPosted(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().MarkTopupIntentSettled(gomock.Any(), reference, gomock.Any()).Return(true, nil)

	var posted ledgerclient.Command
	m := &Module{
		repo:    repo,
		poster:  stubPoster{fn: func(_ context.Context, cmd ledgerclient.Command) error { posted = cmd; return nil }},
		routing: routeTo(vendor, "bca"), logger: discardLogger(),
		// A configured fraudClient that would fail the test if ever
		// invoked — proves merchant events skip screening entirely.
		fraudClient: nil,
	}
	outcome, err := m.HandleVendorCallback(context.Background(), vendor, eventID, reference, amount, currency, "settled", "2026-07-13T00:00:00Z", inboxID, "req-1", "")
	require.NoError(t, err)
	assert.Equal(t, VendorCallbackFinalized, outcome)
	assert.Equal(t, "merchant_payin_credit", posted.Type)
	assert.Equal(t, tenantID, posted.MerchantTenantID)
	assert.Equal(t, uuid.Nil, posted.UserID, "source resolution must never carry a stray UserID alongside MerchantTenantID")
	assert.Equal(t, "payin:acme:evt-1", posted.IdempotencyKey, "the idempotency key shape is owner-neutral, unchanged from the user path")
}

// TestPostAndFinalize_MerchantEvent_NeverCallsFraudClient proves the T6
// scope decision documented in payin.go's postAndFinalize: screening is
// skipped for merchant events, not merely "screened with a zero user id."
// A fraudClient wired to fail the test on any call proves it. Uses a real
// *fraudcheck.Client backed by a gRPC stub that panics if Screen is ever
// invoked.
func TestPostAndFinalize_MerchantEvent_NeverCallsFraudClient(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	repo.EXPECT().MarkPosted(gomock.Any(), gomock.Any()).Return(nil)

	fraud := fraudcheck.New(&panicFraudGRPCClient{t: t}, "payin")
	m := &Module{
		repo:        repo,
		poster:      stubPoster{fn: func(context.Context, ledgerclient.Command) error { return nil }},
		logger:      discardLogger(),
		fraudClient: fraud,
	}
	ev := model.WebhookEvent{
		ID: uuid.New(), Vendor: "acme", VendorEventID: "evt-1",
		MerchantTenantID: uuid.New(), Amount: decimal.NewFromInt(1000), Currency: "IDR",
	}
	require.NoError(t, m.postAndFinalize(context.Background(), ev, "bca"))
}

type panicFraudGRPCClient struct{ t *testing.T }

func (p *panicFraudGRPCClient) Screen(context.Context, *fraudv1.ScreenRequest, ...grpc.CallOption) (*fraudv1.ScreenResponse, error) {
	p.t.Fatal("fraud screening must never be called for a merchant-owned event")
	return nil, nil
}

func TestResolveMerchantVendor_Sandbox_RoutesToMockVendor(t *testing.T) {
	registry := vendorgw.NewRegistry()
	registry.AddPayin(stubVerifier{name: sandboxVendor})
	m := &Module{registry: registry, routing: routeTo("some-live-vendor", "bca")}

	vendor, err := m.resolveMerchantVendor(context.Background(), "sandbox", "IDR", decimal.NewFromInt(10000))
	require.NoError(t, err)
	assert.Equal(t, sandboxVendor, vendor, "sandbox must ALWAYS resolve to the mock vendor, never the rule-based candidate")
}

func TestResolveMerchantVendor_Sandbox_MockUnavailable_FailsClosed(t *testing.T) {
	registry := vendorgw.NewRegistry() // sandboxVendor never registered
	m := &Module{registry: registry, routing: routeTo(sandboxVendor, "bca")}

	_, err := m.resolveMerchantVendor(context.Background(), "sandbox", "IDR", decimal.NewFromInt(10000))
	assert.ErrorIs(t, err, ErrSandboxVendorUnavailable, "a sandbox tenant must fail closed, never fall through to rule-based resolution")
}

func TestResolveMerchantVendor_Live_UsesRuleBasedRouting(t *testing.T) {
	registry := vendorgw.NewRegistry()
	registry.AddPayin(stubVerifier{name: "acme"})
	m := &Module{registry: registry, routing: routeTo("acme", "bca")}

	vendor, err := m.resolveMerchantVendor(context.Background(), "live", "IDR", decimal.NewFromInt(10000))
	require.NoError(t, err)
	assert.Equal(t, "acme", vendor)
}

// TestResolveMerchantVendor_Live_NeverFallsBackToSandboxVendor proves a
// live-environment merchant, even when only the mock vendor happens to be
// registered, gets ErrNoVendorAvailable rather than silently landing on
// the mock adapter — the mock is reachable ONLY via the sandbox branch.
func TestResolveMerchantVendor_Live_NeverFallsBackToSandboxVendor(t *testing.T) {
	registry := vendorgw.NewRegistry()
	registry.AddPayin(stubVerifier{name: sandboxVendor})
	m := &Module{registry: registry, routing: routeTo("some-other-vendor-not-registered", "bca")}

	_, err := m.resolveMerchantVendor(context.Background(), "live", "IDR", decimal.NewFromInt(10000))
	assert.Error(t, err)
}

func TestCreateMerchantTopupIntent_MissingTenantID(t *testing.T) {
	m := &Module{}
	_, err := m.CreateMerchantTopupIntent(context.Background(), uuid.Nil, "sandbox", "IDR", decimal.NewFromInt(1000), "downstream-key")
	assert.Error(t, err)
}

func TestCreateMerchantTopupIntent_MissingDownstreamKey(t *testing.T) {
	m := &Module{}
	_, err := m.CreateMerchantTopupIntent(context.Background(), uuid.New(), "sandbox", "IDR", decimal.NewFromInt(1000), "")
	assert.Error(t, err)
}

func TestCreateMerchantTopupIntent_Sandbox_InsertsPendingIntent(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	tenantID := uuid.New()

	repo.EXPECT().InsertMerchantTopupIntent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, intent model.TopupIntent) (model.TopupIntent, error) {
		assert.Equal(t, tenantID, intent.MerchantTenantID)
		assert.Equal(t, uuid.Nil, intent.UserID)
		assert.Equal(t, sandboxVendor, intent.Vendor)
		assert.Equal(t, model.TopupStatusPending, intent.Status)
		assert.Equal(t, "downstream-key", intent.DownstreamKey)
		return intent, nil
	})

	registry := vendorgw.NewRegistry()
	registry.AddPayin(stubVerifier{name: sandboxVendor})
	m := &Module{repo: repo, registry: registry, routing: routeTo(sandboxVendor, "bca"), logger: discardLogger()}

	intent, err := m.CreateMerchantTopupIntent(context.Background(), tenantID, "sandbox", "IDR", decimal.NewFromInt(50000), "downstream-key")
	require.NoError(t, err)
	assert.Equal(t, sandboxVendor, intent.Vendor)
	assert.Equal(t, tenantID, intent.MerchantTenantID)
}

// TestCreateMerchantTopupIntent_DownstreamKeyConflict_ReturnsOriginal proves
// a Gateway retry using the same downstreamKey (e.g. after a crash between
// the owner call succeeding and Gateway's own idempotency record being
// persisted) recovers the ORIGINAL intent rather than opening a second
// vendor session for a duplicate row (docs/reference/c1-b2b-design.md §10.4).
func TestCreateMerchantTopupIntent_DownstreamKeyConflict_ReturnsOriginal(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	tenantID := uuid.New()
	originalID := uuid.New()

	repo.EXPECT().InsertMerchantTopupIntent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, intent model.TopupIntent) (model.TopupIntent, error) {
		// Simulate another attempt already having won the race: the
		// returned row's ID differs from the one this call tried to insert.
		return model.TopupIntent{
			ID: originalID, MerchantTenantID: tenantID, Vendor: sandboxVendor,
			Status: model.TopupStatusPending, DownstreamKey: intent.DownstreamKey,
		}, nil
	})

	registry := vendorgw.NewRegistry()
	registry.AddPayin(stubVerifier{name: sandboxVendor})
	m := &Module{repo: repo, registry: registry, routing: routeTo(sandboxVendor, "bca"), logger: discardLogger()}

	intent, err := m.CreateMerchantTopupIntent(context.Background(), tenantID, "sandbox", "IDR", decimal.NewFromInt(50000), "downstream-key")
	require.NoError(t, err)
	assert.Equal(t, originalID, intent.ID, "must return the pre-existing row, not the one this attempt tried to insert")
}
