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

func TestMoneyOutResolveAccounts_RequireGatewayError(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)
	p := NewMoneyOut(repo)

	cmd := Command{}

	_, _, err := p.ResolveAccounts(context.Background(), cmd)

	assert.Error(t, err)
}

func TestMoneyOutResolveAccounts_CashError(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)

	repo.EXPECT().
		GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).
		Return(uuid.Nil, errors.New("db"))

	p := NewMoneyOut(repo)

	cmd := Command{
		UserID: uuid.New(),
		Metadata: map[string]any{
			"gateway": "bca",
		},
	}

	_, _, err := p.ResolveAccounts(context.Background(), cmd)

	assert.Error(t, err)
}

func TestMoneyOutResolveAccounts_PocketError(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)

	repo.EXPECT().
		GetPocketAccountID(gomock.Any(), gomock.Any(), "saving").
		Return(uuid.Nil, errors.New("pocket error"))

	p := NewMoneyOut(repo)

	cmd := Command{
		UserID:     uuid.New(),
		PocketCode: "saving",
		Metadata: map[string]any{
			"gateway": "bca",
		},
	}

	_, _, err := p.ResolveAccounts(context.Background(), cmd)

	assert.Error(t, err)
}

func TestMoneyOutResolveAccounts_SystemAccountError(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)

	srcID := uuid.New()

	repo.EXPECT().
		GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).
		Return(srcID, nil)

	repo.EXPECT().
		GetAccountCurrency(gomock.Any(), srcID).
		Return("IDR", nil)

	repo.EXPECT().
		GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").
		Return(uuid.Nil, errors.New("sys error"))

	p := NewMoneyOut(repo)

	cmd := Command{
		UserID: uuid.New(),
		Metadata: map[string]any{
			"gateway": "bca",
		},
	}

	_, _, err := p.ResolveAccounts(context.Background(), cmd)

	assert.Error(t, err)
}

func TestMoneyOutResolveAccounts_CurrencyError(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)

	srcID := uuid.New()

	repo.EXPECT().
		GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).
		Return(srcID, nil)

	repo.EXPECT().
		GetAccountCurrency(gomock.Any(), srcID).
		Return("", errors.New("currency error"))

	p := NewMoneyOut(repo)

	cmd := Command{
		UserID: uuid.New(),
		Metadata: map[string]any{
			"gateway": "bca",
		},
	}

	_, _, err := p.ResolveAccounts(context.Background(), cmd)

	assert.Error(t, err)
}

func TestMoneyOutResolveAccounts_SuccessCash(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)

	srcID := uuid.New()
	sysID := uuid.New()

	repo.EXPECT().
		GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).
		Return(srcID, nil)

	repo.EXPECT().
		GetAccountCurrency(gomock.Any(), srcID).
		Return("IDR", nil)

	repo.EXPECT().
		GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").
		Return(sysID, nil)

	p := NewMoneyOut(repo)

	cmd := Command{
		UserID: uuid.New(),
		Metadata: map[string]any{
			"gateway": "bca",
		},
	}

	ids, cur, err := p.ResolveAccounts(context.Background(), cmd)

	assert.NoError(t, err)
	assert.Len(t, ids.Ordered, 2)
	assert.Equal(t, "IDR", cur)
}

func TestMoneyOutResolveAccounts_WithFee(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)

	srcID := uuid.New()
	sysID := uuid.New()
	feeID := uuid.New()

	repo.EXPECT().
		GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).
		Return(srcID, nil)

	repo.EXPECT().
		GetAccountCurrency(gomock.Any(), srcID).
		Return("IDR", nil)

	repo.EXPECT().
		GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").
		Return(sysID, nil)

	repo.EXPECT().
		GetSystemAccountID(gomock.Any(), constant.AccountTypeFee, "bca", "IDR").
		Return(feeID, nil)

	p := NewMoneyOut(repo)

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

func TestMoneyOutResolveAccounts_WithFeeZero(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)

	srcID := uuid.New()
	sysID := uuid.New()

	repo.EXPECT().
		GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).
		Return(srcID, nil)

	repo.EXPECT().
		GetAccountCurrency(gomock.Any(), srcID).
		Return("IDR", nil)

	repo.EXPECT().
		GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").
		Return(sysID, nil)

	p := NewMoneyOut(repo)

	cmd := Command{
		UserID: uuid.New(),
		Metadata: map[string]any{
			"gateway":     "bca",
			"fee_amount":  "0",
			"fee_gateway": "bca",
		},
	}

	ids, _, err := p.ResolveAccounts(context.Background(), cmd)

	assert.NoError(t, err)
	assert.Len(t, ids.Ordered, 2)
}

func TestMoneyOutResolveAccounts_WithFeeError(t *testing.T) {

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)

	srcID := uuid.New()
	sysID := uuid.New()

	repo.EXPECT().
		GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).
		Return(srcID, nil)

	repo.EXPECT().
		GetAccountCurrency(gomock.Any(), srcID).
		Return("IDR", nil)

	repo.EXPECT().
		GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").
		Return(sysID, nil)

	p := NewMoneyOut(repo)

	cmd := Command{
		UserID: uuid.New(),
		Metadata: map[string]any{
			"gateway":     "bca",
			"fee_amount":  "-10",
			"fee_gateway": "bca",
		},
	}

	ids, _, err := p.ResolveAccounts(context.Background(), cmd)

	assert.Error(t, err)
	assert.Len(t, ids.Ordered, 0)
}

func TestMoneyOutValidate_Success(t *testing.T) {

	p := NewMoneyOut(nil)

	accID := uuid.New()

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(10),
		},
		AccountIDs: []uuid.UUID{accID},
	}

	bal := map[uuid.UUID]model.AccountBalance{
		accID: {Balance: decimal.NewFromInt(100)},
	}

	err := p.Validate(context.Background(), nil, cmd, bal)

	assert.NoError(t, err)
}

func TestMoneyOutValidate_InsufficientFunds(t *testing.T) {

	p := NewMoneyOut(nil)

	accID := uuid.New()

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
		},
		AccountIDs: []uuid.UUID{accID},
	}

	bal := map[uuid.UUID]model.AccountBalance{
		accID: {Balance: decimal.NewFromInt(10)},
	}

	err := p.Validate(context.Background(), nil, cmd, bal)

	assert.Error(t, err)
}

func TestMoneyOutValidate_InsufficientFunds_WithFee(t *testing.T) {

	p := NewMoneyOut(nil)

	accID := uuid.New()
	sysID := uuid.New()
	feeID := uuid.New()

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
			Metadata: map[string]any{
				"gateway":     "bca",
				"fee_amount":  "10",
				"fee_gateway": "bca",
			},
		},
		AccountIDs: []uuid.UUID{accID, sysID, feeID},
		Currency:   "IDR",
	}

	bal := map[uuid.UUID]model.AccountBalance{
		accID: {Balance: decimal.NewFromInt(10)},
	}

	err := p.Validate(context.Background(), nil, cmd, bal)

	assert.Error(t, err)
}

func TestMoneyOutValidate_InsufficientFunds_WithFeeZero(t *testing.T) {

	p := NewMoneyOut(nil)

	accID := uuid.New()
	sysID := uuid.New()
	feeID := uuid.New()

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
			Metadata: map[string]any{
				"gateway":     "bca",
				"fee_amount":  "0",
				"fee_gateway": "bca",
			},
		},
		AccountIDs: []uuid.UUID{accID, sysID, feeID},
		Currency:   "IDR",
	}

	bal := map[uuid.UUID]model.AccountBalance{
		accID: {Balance: decimal.NewFromInt(10)},
	}

	err := p.Validate(context.Background(), nil, cmd, bal)

	assert.Error(t, err)
}

func TestMoneyOutValidate_InsufficientFunds_WithFeeError(t *testing.T) {

	p := NewMoneyOut(nil)

	accID := uuid.New()
	sysID := uuid.New()
	feeID := uuid.New()

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
			Metadata: map[string]any{
				"gateway":     "bca",
				"fee_gateway": "bca",
			},
		},
		AccountIDs: []uuid.UUID{accID, sysID, feeID},
		Currency:   "IDR",
	}

	bal := map[uuid.UUID]model.AccountBalance{
		accID: {Balance: decimal.NewFromInt(10)},
	}

	err := p.Validate(context.Background(), nil, cmd, bal)

	assert.Error(t, err)
}

func TestMoneyOutBuildEntries_WithPocketCode(t *testing.T) {

	p := NewMoneyOut(nil)

	cmd := ResolvedCommand{
		Command: Command{
			Amount:     decimal.NewFromInt(100),
			PocketCode: "saving",
		},
		AccountIDs: []uuid.UUID{uuid.New(), uuid.New()},
	}

	entries, err := p.BuildEntries(context.Background(), nil, cmd, nil)

	assert.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestMoneyOutBuildEntries_NoFee(t *testing.T) {

	p := NewMoneyOut(nil)

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
		},
		AccountIDs: []uuid.UUID{uuid.New(), uuid.New()},
	}

	entries, err := p.BuildEntries(context.Background(), nil, cmd, nil)

	assert.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestMoneyOutBuildEntries_WithFee(t *testing.T) {

	p := NewMoneyOut(nil)

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
			Metadata: map[string]any{
				"fee_amount": "10",
			},
		},
		AccountIDs: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()},
	}

	entries, err := p.BuildEntries(context.Background(), nil, cmd, nil)

	assert.NoError(t, err)
	assert.Len(t, entries, 3)
}

func TestMoneyOutOutboxEvents(t *testing.T) {
	accID := uuid.New()
	sysID := uuid.New()
	feeID := uuid.New()

	p := NewMoneyOut(nil)

	cmd := ResolvedCommand{
		AccountIDs: []uuid.UUID{accID, sysID, feeID},
		Currency:   "IDR",
		Command: Command{
			UserID: uuid.New(),
			Amount: decimal.NewFromInt(100),
			Metadata: map[string]any{
				"gateway":     "bca",
				"fee_amount":  "10",
				"fee_gateway": "bca",
			},
			PocketCode: "saving",
		},
	}

	entries := []model.EntryInstruction{
		{AccountID: accID, Direction: constant.Debit, Amount: decimal.NewFromInt(100)},
		{AccountID: sysID, Direction: constant.Credit, Amount: decimal.NewFromInt(90)},
		{AccountID: feeID, Direction: constant.Credit, Amount: decimal.NewFromInt(10)},
	}
	ev := p.OutboxEvents(cmd, uuid.New(), entries)

	assert.Len(t, ev, 1)
	assert.Equal(t, events.TypeTransactionPosted, ev[0].EventType)
}

func TestMoneyOutAfterCommit(t *testing.T) {

	p := NewMoneyOut(nil)

	err := p.AfterCommit(context.Background(), Command{})

	assert.NoError(t, err)
}
