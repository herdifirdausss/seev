package processors

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func TestMoneyInResolveAccounts_RequireGatewayError(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)
	p := NewMoneyIn(repo)

	cmd := Command{}

	_, _, err := p.ResolveAccounts(context.Background(), cmd)

	assert.Error(t, err)
}

func TestMoneyInResolveAccounts_SystemAccountError(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)

	accID := uuid.New()
	repo.EXPECT().
		GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).
		Return(accID, nil)
	repo.EXPECT().
		GetAccountCurrency(gomock.Any(), accID).
		Return("IDR", nil)
	repo.EXPECT().
		GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").
		Return(uuid.Nil, errors.New("db"))

	p := NewMoneyIn(repo)

	cmd := Command{
		Metadata: map[string]any{
			"gateway": "bca",
		},
	}

	_, _, err := p.ResolveAccounts(context.Background(), cmd)

	assert.Error(t, err)
}

func TestMoneyInResolveAccounts_CashSuccess(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)

	sysID := uuid.New()
	accID := uuid.New()

	repo.EXPECT().
		GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).
		Return(accID, nil)

	repo.EXPECT().
		GetAccountCurrency(gomock.Any(), accID).
		Return("IDR", nil)

	repo.EXPECT().
		GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").
		Return(sysID, nil)

	p := NewMoneyIn(repo)

	cmd := Command{
		UserID: uuid.New(),
		Metadata: map[string]any{
			"gateway": "bca",
		},
	}

	ids, currency, err := p.ResolveAccounts(context.Background(), cmd)

	assert.NoError(t, err)
	assert.Len(t, ids.Ordered, 2)
	assert.Equal(t, "IDR", currency)
}

func TestMoneyInResolveAccounts_PocketSuccess(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)

	sysID := uuid.New()
	pocketID := uuid.New()

	repo.EXPECT().
		GetPocketAccountID(gomock.Any(), gomock.Any(), "saving").
		Return(pocketID, nil)

	repo.EXPECT().
		GetAccountCurrency(gomock.Any(), pocketID).
		Return("IDR", nil)

	repo.EXPECT().
		GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").
		Return(sysID, nil)

	p := NewMoneyIn(repo)

	cmd := Command{
		UserID:     uuid.New(),
		PocketCode: "saving",
		Metadata: map[string]any{
			"gateway": "bca",
		},
	}

	ids, _, err := p.ResolveAccounts(context.Background(), cmd)

	assert.NoError(t, err)
	assert.Len(t, ids.Ordered, 2)
}

func TestMoneyInResolveAccounts_WithFee(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)

	sysID := uuid.New()
	accID := uuid.New()
	feeID := uuid.New()

	repo.EXPECT().
		GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).
		Return(accID, nil)

	repo.EXPECT().
		GetAccountCurrency(gomock.Any(), accID).
		Return("IDR", nil)

	repo.EXPECT().
		GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").
		Return(sysID, nil)

	repo.EXPECT().
		GetSystemAccountID(gomock.Any(), constant.AccountTypeFee, "bca", "IDR").
		Return(feeID, nil)

	p := NewMoneyIn(repo)

	cmd := Command{
		UserID: uuid.New(),
		Metadata: map[string]any{
			"gateway":     "bca",
			"fee_amount":  "10",
			"fee_gateway": "bca",
		},
	}

	ids, _, err := p.ResolveAccounts(context.Background(), cmd)

	assert.NoError(t, err)
	assert.Len(t, ids.Ordered, 3)
}

func TestMoneyInValidate_NoFee(t *testing.T) {

	p := NewMoneyIn(nil)

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
		},
	}

	err := p.Validate(context.Background(), nil, cmd, nil)

	assert.NoError(t, err)
}

func TestMoneyInValidate_WithFee(t *testing.T) {

	p := NewMoneyIn(nil)

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
			Metadata: map[string]any{
				"fee_amount": "10",
			},
		},
		AccountIDs: []uuid.UUID{
			uuid.New(),
			uuid.New(),
			uuid.New(),
		},
	}

	err := p.Validate(context.Background(), nil, cmd, nil)

	assert.NoError(t, err)
}

func TestMoneyInBuildEntries_NoFee(t *testing.T) {

	p := NewMoneyIn(nil)

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
		},
		AccountIDs: []uuid.UUID{
			uuid.New(),
			uuid.New(),
		},
	}

	entries, err := p.BuildEntries(context.Background(), nil, cmd, nil)

	assert.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Equal(t, constant.Debit, entries[0].Direction)
}

func TestMoneyInBuildEntries_WithFee(t *testing.T) {

	p := NewMoneyIn(nil)

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
			Metadata: map[string]any{
				"fee_amount": "10",
			},
		},
		AccountIDs: []uuid.UUID{
			uuid.New(),
			uuid.New(),
			uuid.New(),
		},
	}

	entries, err := p.BuildEntries(context.Background(), nil, cmd, nil)

	assert.NoError(t, err)
	assert.Len(t, entries, 3)
}

func TestMoneyInOutboxEvents(t *testing.T) {

	p := NewMoneyIn(nil)

	cash := uuid.New()
	cmd := ResolvedCommand{
		Command: Command{
			UserID: uuid.New(),
			Amount: decimal.NewFromInt(100),
		},
		Currency:    "IDR",
		Destination: cash,
	}
	entries := []model.EntryInstruction{
		{AccountID: uuid.New(), Direction: constant.Debit, Amount: decimal.NewFromInt(100)},
		{AccountID: cash, Direction: constant.Credit, Amount: decimal.NewFromInt(100)},
	}

	outboxEvents := p.OutboxEvents(cmd, uuid.New(), entries)

	assert.Len(t, outboxEvents, 1)
	assert.Equal(t, events.TypeTransactionPosted, outboxEvents[0].EventType)
}

func TestMoneyInAfterCommit(t *testing.T) {

	p := NewMoneyIn(nil)

	err := p.AfterCommit(context.Background(), Command{})

	assert.NoError(t, err)
}
