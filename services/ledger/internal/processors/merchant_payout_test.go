package processors

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/constants"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

// ── MerchantPayoutHold ───────────────────────────────────────────────────

func TestMerchantPayoutHoldResolveAccounts_MissingTenantID(t *testing.T) {
	p := NewMerchantPayoutHold(nil)
	_, _, err := p.ResolveAccounts(context.Background(), Command{})
	assert.Error(t, err)
}

func TestMerchantPayoutHoldResolveAccounts_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := repository.NewMockAccountRepository(ctrl)
	tenantID := uuid.New()
	cashID, holdID := uuid.New(), uuid.New()

	repo.EXPECT().GetMerchantAccountID(gomock.Any(), tenantID, constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetMerchantAccountID(gomock.Any(), tenantID, constant.AccountTypeHold).Return(holdID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("IDR", nil)

	p := NewMerchantPayoutHold(repo)
	accounts, currency, err := p.ResolveAccounts(context.Background(), Command{MerchantTenantID: tenantID})
	assert.NoError(t, err)
	assert.Equal(t, cashID, accounts.Source)
	assert.Equal(t, holdID, accounts.Destination)
	assert.Equal(t, "IDR", currency)
}

func TestMerchantPayoutHoldValidate_InsufficientFunds(t *testing.T) {
	p := NewMerchantPayoutHold(nil)
	source, dest := uuid.New(), uuid.New()
	cmd := ResolvedCommand{
		Command:    Command{Amount: decimal.NewFromInt(1000)},
		AccountIDs: []uuid.UUID{source, dest},
	}
	balances := map[uuid.UUID]model.AccountBalance{
		source: {AccountID: source, Balance: decimal.NewFromInt(500), Status: constant.AccountStatusActive},
		dest:   {AccountID: dest, Balance: decimal.Zero, Status: constant.AccountStatusActive},
	}
	assert.Error(t, p.Validate(context.Background(), nil, cmd, balances))
}

func TestMerchantPayoutHoldBuildEntries(t *testing.T) {
	p := NewMerchantPayoutHold(nil)
	source, dest := uuid.New(), uuid.New()
	cmd := ResolvedCommand{
		Command:    Command{Amount: decimal.NewFromInt(20000)},
		AccountIDs: []uuid.UUID{source, dest},
	}
	entries, err := p.BuildEntries(context.Background(), nil, cmd, nil)
	assert.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestMerchantPayoutHoldValidateCommand(t *testing.T) {
	p := NewMerchantPayoutHold(nil)
	assert.ErrorIs(t, p.ValidateCommand(context.Background(), Command{}), apperror.ErrValidation)
	assert.NoError(t, p.ValidateCommand(context.Background(), Command{MerchantTenantID: uuid.New()}))
}

func TestMerchantPayoutHoldType(t *testing.T) {
	assert.Equal(t, "merchant_payout_hold", NewMerchantPayoutHold(nil).Type())
}

// ── MerchantPayoutSettle ─────────────────────────────────────────────────

func TestMerchantPayoutSettleResolveAccounts_MissingTenantID(t *testing.T) {
	p := NewMerchantPayoutSettle(nil, nil)
	_, _, err := p.ResolveAccounts(context.Background(), Command{Metadata: map[string]any{"gateway": "bca"}})
	assert.Error(t, err)
}

func TestMerchantPayoutSettleResolveAccounts_MissingGateway(t *testing.T) {
	p := NewMerchantPayoutSettle(nil, nil)
	_, _, err := p.ResolveAccounts(context.Background(), Command{MerchantTenantID: uuid.New()})
	assert.Error(t, err)
}

func TestMerchantPayoutSettleResolveAccounts_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := repository.NewMockAccountRepository(ctrl)
	tenantID := uuid.New()
	holdID, sysID := uuid.New(), uuid.New()

	repo.EXPECT().GetMerchantAccountID(gomock.Any(), tenantID, constant.AccountTypeHold).Return(holdID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), holdID).Return("IDR", nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").Return(sysID, nil)

	p := NewMerchantPayoutSettle(repo, nil)
	accounts, currency, err := p.ResolveAccounts(context.Background(), Command{
		MerchantTenantID: tenantID,
		Metadata:         map[string]any{"gateway": "bca"},
	})
	assert.NoError(t, err)
	assert.Equal(t, holdID, accounts.Source)
	assert.Equal(t, sysID, accounts.Destination)
	assert.Equal(t, "IDR", currency)
}

func TestMerchantPayoutSettleValidateCommand_RequiresReferenceID(t *testing.T) {
	p := NewMerchantPayoutSettle(nil, nil)
	assert.ErrorIs(t, p.ValidateCommand(context.Background(), Command{}), apperror.ErrValidation)
	assert.Error(t, p.ValidateCommand(context.Background(), Command{MerchantTenantID: uuid.New()}), "ReferenceID must still be required")
	assert.NoError(t, p.ValidateCommand(context.Background(), Command{MerchantTenantID: uuid.New(), ReferenceID: uuid.New()}))
}

func TestMerchantPayoutSettleBuildEntries(t *testing.T) {
	p := NewMerchantPayoutSettle(nil, nil)
	source, dest := uuid.New(), uuid.New()
	cmd := ResolvedCommand{
		Command:    Command{Amount: decimal.NewFromInt(20000)},
		AccountIDs: []uuid.UUID{source, dest},
	}
	entries, err := p.BuildEntries(context.Background(), nil, cmd, nil)
	assert.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestMerchantPayoutSettleType(t *testing.T) {
	assert.Equal(t, "merchant_payout_settle", NewMerchantPayoutSettle(nil, nil).Type())
}

// ── MerchantPayoutCancel ─────────────────────────────────────────────────

func TestMerchantPayoutCancelResolveAccounts_MissingTenantID(t *testing.T) {
	p := NewMerchantPayoutCancel(nil, nil)
	_, _, err := p.ResolveAccounts(context.Background(), Command{})
	assert.Error(t, err)
}

func TestMerchantPayoutCancelResolveAccounts_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := repository.NewMockAccountRepository(ctrl)
	tenantID := uuid.New()
	holdID, cashID := uuid.New(), uuid.New()

	repo.EXPECT().GetMerchantAccountID(gomock.Any(), tenantID, constant.AccountTypeHold).Return(holdID, nil)
	repo.EXPECT().GetMerchantAccountID(gomock.Any(), tenantID, constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), holdID).Return("IDR", nil)

	p := NewMerchantPayoutCancel(repo, nil)
	accounts, currency, err := p.ResolveAccounts(context.Background(), Command{MerchantTenantID: tenantID})
	assert.NoError(t, err)
	assert.Equal(t, holdID, accounts.Source)
	assert.Equal(t, cashID, accounts.Destination)
	assert.Equal(t, "IDR", currency)
}

func TestMerchantPayoutCancelValidateCommand_RequiresReferenceID(t *testing.T) {
	p := NewMerchantPayoutCancel(nil, nil)
	assert.Error(t, p.ValidateCommand(context.Background(), Command{MerchantTenantID: uuid.New()}))
	assert.NoError(t, p.ValidateCommand(context.Background(), Command{MerchantTenantID: uuid.New(), ReferenceID: uuid.New()}))
}

func TestMerchantPayoutCancelBuildEntries(t *testing.T) {
	p := NewMerchantPayoutCancel(nil, nil)
	source, dest := uuid.New(), uuid.New()
	cmd := ResolvedCommand{
		Command:    Command{Amount: decimal.NewFromInt(5000)},
		AccountIDs: []uuid.UUID{source, dest},
	}
	entries, err := p.BuildEntries(context.Background(), nil, cmd, nil)
	assert.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.True(t, entries[0].Amount.Equal(entries[1].Amount), "cancel refunds the full held amount, no fee")
}

func TestMerchantPayoutCancelType(t *testing.T) {
	assert.Equal(t, "merchant_payout_cancel", NewMerchantPayoutCancel(nil, nil).Type())
}
