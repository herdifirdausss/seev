package processors

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestWithdrawSettleResolveAccounts_NoFee(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)
	holdID := uuid.New()
	sysID := uuid.New()

	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeHold).Return(holdID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), holdID).Return("IDR", nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").Return(sysID, nil)

	p := NewWithdrawSettle(repo, nil)
	cmd := Command{Metadata: map[string]any{"gateway": "bca"}}

	resolved, currency, err := p.ResolveAccounts(context.Background(), cmd)

	require.NoError(t, err)
	assert.Equal(t, "IDR", currency)
	assert.Len(t, resolved.Ordered, 2, "no fee metadata must resolve exactly 2 legs (hold, settlement)")
}

// TestWithdrawSettleResolveAccounts_WithFee proves docs/roadmap/archive/25 Task T2's
// withdraw fee resolves a 3rd fee[gateway] leg — mirrors money_in's own
// TestMoneyInResolveAccounts_WithFee.
func TestWithdrawSettleResolveAccounts_WithFee(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := repository.NewMockAccountRepository(ctrl)
	holdID := uuid.New()
	sysID := uuid.New()
	feeID := uuid.New()

	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeHold).Return(holdID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), holdID).Return("IDR", nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").Return(sysID, nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeFee, "platform", "IDR").Return(feeID, nil)

	p := NewWithdrawSettle(repo, nil)
	cmd := Command{
		UserID: uuid.New(),
		Metadata: map[string]any{
			"gateway":     "bca",
			"fee_amount":  "2500",
			"fee_gateway": "platform",
		},
	}

	resolved, _, err := p.ResolveAccounts(context.Background(), cmd)

	require.NoError(t, err)
	assert.Len(t, resolved.Ordered, 3, "fee metadata must resolve exactly 3 legs (hold, settlement, fee)")
}

func TestWithdrawSettleBuildEntries_NoFee(t *testing.T) {
	p := NewWithdrawSettle(nil, nil)
	cmd := ResolvedCommand{
		Command:    Command{Amount: decimal.NewFromInt(100_000)},
		AccountIDs: []uuid.UUID{uuid.New(), uuid.New()},
	}

	entries, err := p.BuildEntries(context.Background(), nil, cmd, nil)

	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, constant.Debit, entries[0].Direction)
	assert.True(t, entries[0].Amount.Equal(decimal.NewFromInt(100_000)), "hold debit must be the full amount")
	assert.Equal(t, constant.Credit, entries[1].Direction)
	assert.True(t, entries[1].Amount.Equal(decimal.NewFromInt(100_000)), "settlement credit must be the full amount with no fee")
}

// TestWithdrawSettleBuildEntries_WithFee proves deduct-from-amount
// semantics (docs/roadmap/archive/25 Task T2): settlement receives amount−fee, the
// fee account receives fee, hold is still debited the FULL amount — the
// user's withdrawal itself is unchanged, only what arrives at the bank
// rail is reduced.
func TestWithdrawSettleBuildEntries_WithFee(t *testing.T) {
	p := NewWithdrawSettle(nil, nil)
	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100_000),
			Metadata: map[string]any{
				"fee_amount":  "2500",
				"fee_gateway": "platform",
			},
		},
		AccountIDs: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()},
	}

	entries, err := p.BuildEntries(context.Background(), nil, cmd, nil)

	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.True(t, entries[0].Amount.Equal(decimal.NewFromInt(100_000)), "hold debit must be the FULL amount regardless of fee")
	assert.True(t, entries[1].Amount.Equal(decimal.NewFromInt(97_500)), "settlement credit must be amount minus fee")
	assert.True(t, entries[2].Amount.Equal(decimal.NewFromInt(2_500)), "fee account credit must equal the fee")

	sum := entries[1].Amount.Add(entries[2].Amount)
	assert.True(t, sum.Equal(entries[0].Amount), "settlement + fee must equal the debited hold amount — balanced")
}

func TestWithdrawSettleOutboxEvents(t *testing.T) {
	p := NewWithdrawSettle(nil, nil)
	hold := uuid.New()
	settlement := uuid.New()
	cmd := ResolvedCommand{
		Command:     Command{UserID: uuid.New(), Amount: decimal.NewFromInt(100_000)},
		Currency:    "IDR",
		Destination: settlement,
	}
	entries := []model.EntryInstruction{
		{AccountID: hold, Direction: constant.Debit, Amount: decimal.NewFromInt(100_000)},
		{AccountID: settlement, Direction: constant.Credit, Amount: decimal.NewFromInt(100_000)},
	}

	outboxEvents := p.OutboxEvents(cmd, uuid.New(), entries)

	assert.Len(t, outboxEvents, 1)
	assert.Equal(t, events.TypeTransactionPosted, outboxEvents[0].EventType)
}
