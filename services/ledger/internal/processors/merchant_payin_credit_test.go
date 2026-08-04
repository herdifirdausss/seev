package processors

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/constants"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func TestMerchantPayinCreditResolveAccounts_MissingTenantID(t *testing.T) {
	p := NewMerchantPayinCredit(nil)
	_, _, err := p.ResolveAccounts(context.Background(), Command{Metadata: map[string]any{"gateway": "bca"}})
	assert.Error(t, err)
}

func TestMerchantPayinCreditResolveAccounts_MissingGateway(t *testing.T) {
	p := NewMerchantPayinCredit(nil)
	_, _, err := p.ResolveAccounts(context.Background(), Command{MerchantTenantID: uuid.New()})
	assert.Error(t, err)
}

func TestMerchantPayinCreditResolveAccounts_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)
	tenantID := uuid.New()
	destID := uuid.New()
	sysID := uuid.New()

	repo.EXPECT().GetMerchantAccountID(gomock.Any(), tenantID, constant.AccountTypeCash).Return(destID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), destID).Return("IDR", nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").Return(sysID, nil)

	p := NewMerchantPayinCredit(repo)
	accounts, currency, err := p.ResolveAccounts(context.Background(), Command{
		MerchantTenantID: tenantID,
		Metadata:         map[string]any{"gateway": "bca"},
	})
	assert.NoError(t, err)
	assert.Equal(t, sysID, accounts.Source)
	assert.Equal(t, destID, accounts.Destination)
	assert.Equal(t, "IDR", currency)
}

func TestMerchantPayinCreditBuildEntries(t *testing.T) {
	p := NewMerchantPayinCredit(nil)
	source, dest := uuid.New(), uuid.New()
	cmd := ResolvedCommand{
		Command:    Command{Amount: decimal.NewFromInt(50000)},
		AccountIDs: []uuid.UUID{source, dest},
	}
	entries, err := p.BuildEntries(context.Background(), nil, cmd, nil)
	assert.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Equal(t, constant.Debit, entries[0].Direction)
	assert.Equal(t, constant.Credit, entries[1].Direction)
}

func TestMerchantPayinCreditValidateCommand(t *testing.T) {
	p := NewMerchantPayinCredit(nil)
	assert.Error(t, p.ValidateCommand(context.Background(), Command{}))
	assert.ErrorIs(t, p.ValidateCommand(context.Background(), Command{}), apperror.ErrValidation)
	assert.NoError(t, p.ValidateCommand(context.Background(), Command{MerchantTenantID: uuid.New()}))
}

func TestMerchantPayinCreditType(t *testing.T) {
	assert.Equal(t, "merchant_payin_credit", NewMerchantPayinCredit(nil).Type())
}

func TestMerchantPayinCreditAfterCommit(t *testing.T) {
	assert.NoError(t, NewMerchantPayinCredit(nil).AfterCommit(context.Background(), Command{}))
}
