package payout

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/payout/repository"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
)

func sampleMerchantRequest(id, tenantID uuid.UUID, status string) model.PayoutRequest {
	return model.PayoutRequest{
		ID: id, MerchantTenantID: tenantID, Amount: decimal.NewFromInt(100_000), Currency: "IDR",
		Vendor: "mockvendor", Destination: []byte(`{}`), Status: status, CreatedBy: "test",
	}
}

func TestResolveMerchantVendor_Sandbox_RoutesToMockVendor(t *testing.T) {
	registry := vendorgw.NewRegistry()
	registry.AddPayout(&stubPayoutProvider{name: sandboxVendor})
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
	registry.AddPayout(&stubPayoutProvider{name: "acme"})
	m := &Module{registry: registry, routing: routeTo("acme", "bca")}

	vendor, err := m.resolveMerchantVendor(context.Background(), "live", "IDR", decimal.NewFromInt(10000))
	require.NoError(t, err)
	assert.Equal(t, "acme", vendor)
}

func TestResolveMerchantVendor_Live_NeverFallsBackToSandboxVendor(t *testing.T) {
	registry := vendorgw.NewRegistry()
	registry.AddPayout(&stubPayoutProvider{name: sandboxVendor})
	m := &Module{registry: registry, routing: routeTo("some-other-vendor-not-registered", "bca")}

	_, err := m.resolveMerchantVendor(context.Background(), "live", "IDR", decimal.NewFromInt(10000))
	assert.Error(t, err)
}

func TestCreateMerchant_MissingTenantID(t *testing.T) {
	m := &Module{}
	_, err := m.CreateMerchant(context.Background(), uuid.Nil, "sandbox", "IDR", decimal.NewFromInt(1000), []byte(`{}`), "test")
	assert.Error(t, err)
}

// TestCreateMerchant_HappyPath_PostsMerchantPayoutHold proves the
// merchant-owned Create path posts merchant_payout_hold (never
// withdraw_initiate) with MerchantTenantID set and UserID left at its
// zero sentinel, and — mirroring TestCreate_HappyPath's own assertion —
// never calls the vendor directly (dispatch stays the relay's job alone).
func TestCreateMerchant_HappyPath_PostsMerchantPayoutHold(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	cmdRepo := repository.NewMockVendorCommandRepository(ctrl)
	tenantID := uuid.New()
	holdTxID := uuid.New()

	var posted ledgerclient.Command
	repo.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().TransitionToHeld(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
	cmdRepo.EXPECT().EnqueueInitialSubmit(gomock.Any(), gomock.Any(), sandboxVendor).Return(true, nil)

	registry := vendorgw.NewRegistry()
	registry.AddPayout(&stubPayoutProvider{name: sandboxVendor})
	poster := stubPoster{
		postFn: func(_ context.Context, cmd ledgerclient.Command) error { posted = cmd; return nil },
		getTxFn: func(_ context.Context, _, _ string) (ledgerclient.Transaction, error) {
			return ledgerclient.Transaction{ID: holdTxID}, nil
		},
	}
	m := newTestModule(repo, poster, registry)
	m.commandRepo = cmdRepo

	id, err := m.CreateMerchant(context.Background(), tenantID, "sandbox", "IDR", decimal.NewFromInt(100_000), []byte(`{}`), "test")
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
	assert.Equal(t, "merchant_payout_hold", posted.Type)
	assert.Equal(t, tenantID, posted.MerchantTenantID)
	assert.Equal(t, uuid.Nil, posted.UserID)
}

// TestSettle_MerchantRequest_PostsMerchantPayoutSettle proves settle()
// branches on req.MerchantTenantID exactly like hold() — the merchant
// counterpart of withdraw_settle, never the user-owned type.
func TestSettle_MerchantRequest_PostsMerchantPayoutSettle(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	tenantID := uuid.New()
	holdTxID := uuid.New()
	settleTxID := uuid.New()

	req := sampleMerchantRequest(id, tenantID, model.StatusSubmitted)
	req.HoldTxID = &holdTxID
	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil)
	repo.EXPECT().TransitionToSettled(gomock.Any(), id, settleTxID).Return(true, nil)

	var posted ledgerclient.Command
	poster := stubPoster{
		postFn: func(_ context.Context, cmd ledgerclient.Command) error { posted = cmd; return nil },
		getTxFn: func(_ context.Context, _, _ string) (ledgerclient.Transaction, error) {
			return ledgerclient.Transaction{ID: settleTxID}, nil
		},
	}
	m := newTestModule(repo, poster, vendorgw.NewRegistry())
	require.NoError(t, m.settle(context.Background(), id, "bca"))
	assert.Equal(t, "merchant_payout_settle", posted.Type)
	assert.Equal(t, tenantID, posted.MerchantTenantID)
	assert.Equal(t, uuid.Nil, posted.UserID)
}

// TestCancel_MerchantRequest_PostsMerchantPayoutCancel is settle's mirror
// for cancel().
func TestCancel_MerchantRequest_PostsMerchantPayoutCancel(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	tenantID := uuid.New()
	holdTxID := uuid.New()
	cancelTxID := uuid.New()

	req := sampleMerchantRequest(id, tenantID, model.StatusSubmitted)
	req.HoldTxID = &holdTxID
	repo.EXPECT().Get(gomock.Any(), id).Return(req, nil)
	repo.EXPECT().TransitionToCancelled(gomock.Any(), id, cancelTxID).Return(true, nil)

	var posted ledgerclient.Command
	poster := stubPoster{
		postFn: func(_ context.Context, cmd ledgerclient.Command) error { posted = cmd; return nil },
		getTxFn: func(_ context.Context, _, _ string) (ledgerclient.Transaction, error) {
			return ledgerclient.Transaction{ID: cancelTxID}, nil
		},
	}
	m := newTestModule(repo, poster, vendorgw.NewRegistry())
	require.NoError(t, m.cancel(context.Background(), id, "bca", ""))
	assert.Equal(t, "merchant_payout_cancel", posted.Type)
	assert.Equal(t, tenantID, posted.MerchantTenantID)
	assert.Equal(t, uuid.Nil, posted.UserID)
}
