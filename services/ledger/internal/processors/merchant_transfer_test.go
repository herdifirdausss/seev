package processors

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/contracts/events/ledger"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/constants"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func TestMerchantTransferResolveAccounts_MissingTenantID(t *testing.T) {
	p := NewMerchantTransfer(nil)

	_, _, err := p.ResolveAccounts(context.Background(), Command{
		Metadata: map[string]any{"destination_account_id": uuid.New().String()},
	})

	assert.Error(t, err)
}

func TestMerchantTransferResolveAccounts_MissingDestination(t *testing.T) {
	p := NewMerchantTransfer(nil)

	_, _, err := p.ResolveAccounts(context.Background(), Command{MerchantTenantID: uuid.New()})

	assert.Error(t, err, "destination_account_id must be required, never defaulted")
}

// TestMerchantTransferResolveAccounts_SourceNeverCallerSupplied proves T5's
// own acceptance criterion — "source account cannot be substituted by the
// caller" — there is no field anywhere on Command the caller could set to
// choose their own source; ResolveAccounts calls GetMerchantAccountID with
// EXACTLY MerchantTenantID, never anything read out of Metadata.
func TestMerchantTransferResolveAccounts_SourceNeverCallerSupplied(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)
	tenantID := uuid.New()
	sourceID := uuid.New()
	destID := uuid.New()

	repo.EXPECT().
		GetMerchantAccountID(gomock.Any(), tenantID, constant.AccountTypeCash).
		Return(sourceID, nil)
	repo.EXPECT().
		GetAccountCurrency(gomock.Any(), sourceID).
		Return("IDR", nil)

	p := NewMerchantTransfer(repo)
	cmd := Command{
		MerchantTenantID: tenantID,
		Metadata: map[string]any{
			"destination_account_id": destID.String(),
			// An attacker-controlled Metadata trying to smuggle a source
			// override — MUST be silently ignored, since ResolveAccounts
			// never reads any such key.
			"source_account_id": uuid.New().String(),
			"account_id":        uuid.New().String(),
		},
	}

	accounts, currency, err := p.ResolveAccounts(context.Background(), cmd)
	assert.NoError(t, err)
	assert.Equal(t, sourceID, accounts.Source, "source must be exactly what GetMerchantAccountID(tenantID) resolved")
	assert.Equal(t, destID, accounts.Destination)
	assert.Equal(t, "IDR", currency)
}

func TestMerchantTransferResolveAccounts_SelfTransferRejected(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)
	tenantID := uuid.New()
	sourceID := uuid.New()

	repo.EXPECT().
		GetMerchantAccountID(gomock.Any(), tenantID, constant.AccountTypeCash).
		Return(sourceID, nil)

	p := NewMerchantTransfer(repo)
	_, _, err := p.ResolveAccounts(context.Background(), Command{
		MerchantTenantID: tenantID,
		Metadata:         map[string]any{"destination_account_id": sourceID.String()},
	})

	assert.ErrorIs(t, err, apperror.ErrSelfTransfer)
}

func TestMerchantTransferResolveAccounts_SourceLookupError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)
	tenantID := uuid.New()

	repo.EXPECT().
		GetMerchantAccountID(gomock.Any(), tenantID, constant.AccountTypeCash).
		Return(uuid.Nil, apperror.ErrAccountNotFound)

	p := NewMerchantTransfer(repo)
	_, _, err := p.ResolveAccounts(context.Background(), Command{
		MerchantTenantID: tenantID,
		Metadata:         map[string]any{"destination_account_id": uuid.New().String()},
	})

	assert.ErrorIs(t, err, apperror.ErrAccountNotFound, "an unprovisioned tenant must surface a clean not-found error")
}

func TestMerchantTransferValidate(t *testing.T) {
	p := NewMerchantTransfer(nil)

	source, dest := uuid.New(), uuid.New()
	cmd := ResolvedCommand{
		Command:    Command{Amount: decimal.NewFromInt(1000)},
		AccountIDs: []uuid.UUID{source, dest},
	}
	balances := map[uuid.UUID]model.AccountBalance{
		source: {AccountID: source, Balance: decimal.NewFromInt(5000), Status: constant.AccountStatusActive},
		dest:   {AccountID: dest, Balance: decimal.Zero, Status: constant.AccountStatusActive},
	}

	err := p.Validate(context.Background(), nil, cmd, balances)
	assert.NoError(t, err)
}

func TestMerchantTransferValidate_InsufficientFunds(t *testing.T) {
	p := NewMerchantTransfer(nil)

	source, dest := uuid.New(), uuid.New()
	cmd := ResolvedCommand{
		Command:    Command{Amount: decimal.NewFromInt(1000)},
		AccountIDs: []uuid.UUID{source, dest},
	}
	balances := map[uuid.UUID]model.AccountBalance{
		source: {AccountID: source, Balance: decimal.NewFromInt(500), Status: constant.AccountStatusActive},
		dest:   {AccountID: dest, Balance: decimal.Zero, Status: constant.AccountStatusActive},
	}

	err := p.Validate(context.Background(), nil, cmd, balances)
	assert.Error(t, err)
}

func TestMerchantTransferBuildEntries(t *testing.T) {
	p := NewMerchantTransfer(nil)

	source, dest := uuid.New(), uuid.New()
	cmd := ResolvedCommand{
		Command:    Command{Amount: decimal.NewFromInt(2500)},
		AccountIDs: []uuid.UUID{source, dest},
	}

	entries, err := p.BuildEntries(context.Background(), nil, cmd, nil)
	assert.NoError(t, err)
	assert.Len(t, entries, 2, "no fee support: exactly 2 balanced entries")
	assert.Equal(t, constant.Debit, entries[0].Direction)
	assert.Equal(t, source, entries[0].AccountID)
	assert.Equal(t, constant.Credit, entries[1].Direction)
	assert.Equal(t, dest, entries[1].AccountID)
	assert.True(t, entries[0].Amount.Equal(entries[1].Amount), "debit and credit must be for the identical amount — no implicit fee split")
}

func TestMerchantTransferOutboxEvents_CarriesMerchantTenantID(t *testing.T) {
	p := NewMerchantTransfer(nil)

	tenantID := uuid.New()
	source, dest := uuid.New(), uuid.New()
	cmd := ResolvedCommand{
		Command:     Command{MerchantTenantID: tenantID, Amount: decimal.NewFromInt(100)},
		Currency:    "IDR",
		Source:      source,
		Destination: dest,
	}
	entries := []model.EntryInstruction{
		{AccountID: source, Direction: constant.Debit, Amount: decimal.NewFromInt(100)},
		{AccountID: dest, Direction: constant.Credit, Amount: decimal.NewFromInt(100)},
	}

	outboxEvents := p.OutboxEvents(cmd, uuid.New(), entries)
	assert.Len(t, outboxEvents, 1)
	assert.Equal(t, events.TypeTransactionPosted, outboxEvents[0].EventType)
	payload := outboxEvents[0].Payload
	assert.Equal(t, tenantID.String(), payload["merchant_tenant_id"], "the outbox event must carry the tenant id for T7's webhook routing")
}

func TestMerchantTransferValidateCommand_RequiresTenantID(t *testing.T) {
	p := NewMerchantTransfer(nil)

	assert.Error(t, p.ValidateCommand(context.Background(), Command{}))
	assert.NoError(t, p.ValidateCommand(context.Background(), Command{MerchantTenantID: uuid.New()}))
}

func TestMerchantTransferAfterCommit(t *testing.T) {
	p := NewMerchantTransfer(nil)
	assert.NoError(t, p.AfterCommit(context.Background(), Command{}))
}

func TestMerchantTransferType(t *testing.T) {
	assert.Equal(t, "merchant_transfer", NewMerchantTransfer(nil).Type())
}
