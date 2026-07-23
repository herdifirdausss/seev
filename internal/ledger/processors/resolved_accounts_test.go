package processors

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/internal/ledger/constant"
)

// This file locks the Source/Destination contract (docs/roadmap/archive/14 Task T1,
// decision K2) for every registered processor: Source is always the account
// debited, Destination the account credited, matching each processor's own
// BuildEntries (AccountIDs[0]=Debit, AccountIDs[1]=Credit). One test per
// processor rather than a single table — the mock call sequences genuinely
// differ (different repo methods, different metadata) and forcing them into
// one generic table would obscure more than it clarifies.

func TestResolvedAccounts_MoneyIn(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	settlementID, cashID := uuid.New(), uuid.New()
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").Return(settlementID, nil)
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("IDR", nil)

	p := NewMoneyIn(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New(), Metadata: map[string]any{"gateway": "bca"}})

	require.NoError(t, err)
	assert.Equal(t, settlementID, ra.Source)
	assert.Equal(t, cashID, ra.Destination)
}

func TestResolvedAccounts_MoneyOut(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	cashID, settlementID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").Return(settlementID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("IDR", nil)

	p := NewMoneyOut(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New(), Metadata: map[string]any{"gateway": "bca"}})

	require.NoError(t, err)
	assert.Equal(t, cashID, ra.Source)
	assert.Equal(t, settlementID, ra.Destination)
}

func TestResolvedAccounts_TransferP2P(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	senderID, receiverID := uuid.New(), uuid.New()
	userID, targetID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), userID, constant.AccountTypeCash).Return(senderID, nil)
	repo.EXPECT().GetAccountID(gomock.Any(), targetID, constant.AccountTypeCash).Return(receiverID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), senderID).Return("IDR", nil)

	p := NewTransferP2P(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: userID, TargetUserID: targetID})

	require.NoError(t, err)
	assert.Equal(t, senderID, ra.Source)
	assert.Equal(t, receiverID, ra.Destination)
}

func TestResolvedAccounts_TransferPocket(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	cashID, pocketID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetPocketAccountID(gomock.Any(), gomock.Any(), "savings").Return(pocketID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("IDR", nil)

	p := NewTransferPocket(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New(), PocketCode: "savings"})

	require.NoError(t, err)
	assert.Equal(t, cashID, ra.Source)
	assert.Equal(t, pocketID, ra.Destination)
}

func TestResolvedAccounts_WithdrawInitiate(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	cashID, holdID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeHold).Return(holdID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("IDR", nil)

	p := NewWithdrawInitiate(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New()})

	require.NoError(t, err)
	assert.Equal(t, cashID, ra.Source)
	assert.Equal(t, holdID, ra.Destination)
}

func TestResolvedAccounts_WithdrawPending(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	holdID, pendingID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeHold).Return(holdID, nil)
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypePending).Return(pendingID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), holdID).Return("IDR", nil)

	p := NewWithdrawPending(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New()})

	require.NoError(t, err)
	assert.Equal(t, holdID, ra.Source)
	assert.Equal(t, pendingID, ra.Destination)
}

func TestResolvedAccounts_WithdrawSettle(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	holdID, settlementID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeHold).Return(holdID, nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").Return(settlementID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), holdID).Return("IDR", nil)

	p := NewWithdrawSettle(repo, nil)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New(), Metadata: map[string]any{"gateway": "bca"}})

	require.NoError(t, err)
	assert.Equal(t, holdID, ra.Source)
	assert.Equal(t, settlementID, ra.Destination)
}

func TestResolvedAccounts_WithdrawCancel(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	holdID, cashID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeHold).Return(holdID, nil)
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), holdID).Return("IDR", nil)

	p := NewWithdrawCancel(repo, nil)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New()})

	require.NoError(t, err)
	assert.Equal(t, holdID, ra.Source)
	assert.Equal(t, cashID, ra.Destination)
}

func TestResolvedAccounts_WithdrawPendingSettle(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	pendingID, settlementID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypePending).Return(pendingID, nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "bca", "IDR").Return(settlementID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), pendingID).Return("IDR", nil)

	p := NewWithdrawPendingSettle(repo, nil)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New(), Metadata: map[string]any{"gateway": "bca"}})

	require.NoError(t, err)
	assert.Equal(t, pendingID, ra.Source)
	assert.Equal(t, settlementID, ra.Destination)
}

func TestResolvedAccounts_WithdrawPendingCancel(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	pendingID, cashID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypePending).Return(pendingID, nil)
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), pendingID).Return("IDR", nil)

	p := NewWithdrawPendingCancel(repo, nil)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New()})

	require.NoError(t, err)
	assert.Equal(t, pendingID, ra.Source)
	assert.Equal(t, cashID, ra.Destination)
}

func TestResolvedAccounts_Refund(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	merchantID, userCashID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(userCashID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), userCashID).Return("IDR", nil)

	p := NewRefund(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{
		TargetUserID: uuid.New(),
		Metadata:     map[string]any{"merchant_account_id": merchantID.String()},
	})

	require.NoError(t, err)
	assert.Equal(t, merchantID, ra.Source)
	assert.Equal(t, userCashID, ra.Destination)
}

func TestResolvedAccounts_FeeCollect(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	cashID, feeID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("IDR", nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeFee, "platform", "IDR").Return(feeID, nil)

	p := NewFeeCollect(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New(), Metadata: map[string]any{"fee_gateway": "platform"}})

	require.NoError(t, err)
	assert.Equal(t, cashID, ra.Source)
	assert.Equal(t, feeID, ra.Destination)
}

func TestResolvedAccounts_Chargeback(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	cashID, cbID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeChargeback, "visa", "IDR").Return(cbID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("IDR", nil)

	p := NewChargeback(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{
		UserID:   uuid.New(),
		Metadata: map[string]any{"dispute_ref": "d1", "card_network": "visa"},
	})

	require.NoError(t, err)
	assert.Equal(t, cashID, ra.Source)
	assert.Equal(t, cbID, ra.Destination)
}

func TestResolvedAccounts_EscrowHold(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	cashID, escrowID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("IDR", nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeEscrow, "IDR", "IDR").Return(escrowID, nil)

	p := NewEscrowHold(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New()})

	require.NoError(t, err)
	assert.Equal(t, cashID, ra.Source)
	assert.Equal(t, escrowID, ra.Destination)
}

func TestResolvedAccounts_EscrowRelease(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	merchantID, escrowID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountCurrency(gomock.Any(), merchantID).Return("IDR", nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeEscrow, "IDR", "IDR").Return(escrowID, nil)

	p := NewEscrowRelease(repo, nil)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{Metadata: map[string]any{"merchant_account_id": merchantID.String()}})

	require.NoError(t, err)
	assert.Equal(t, escrowID, ra.Source)
	assert.Equal(t, merchantID, ra.Destination)
}

func TestResolvedAccounts_EscrowRefund(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	buyerCashID, escrowID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(buyerCashID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), buyerCashID).Return("IDR", nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeEscrow, "IDR", "IDR").Return(escrowID, nil)

	p := NewEscrowRefund(repo, nil)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{TargetUserID: uuid.New()})

	require.NoError(t, err)
	assert.Equal(t, escrowID, ra.Source)
	assert.Equal(t, buyerCashID, ra.Destination)
}

func TestResolvedAccounts_FreezeInitiate(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	cashID, frozenID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeFrozen).Return(frozenID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("IDR", nil)

	p := NewFreezeInitiate(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New()})

	require.NoError(t, err)
	assert.Equal(t, cashID, ra.Source)
	assert.Equal(t, frozenID, ra.Destination)
}

func TestResolvedAccounts_FreezeRelease(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	frozenID, cashID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeFrozen).Return(frozenID, nil)
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), frozenID).Return("IDR", nil)

	p := NewFreezeRelease(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New()})

	require.NoError(t, err)
	assert.Equal(t, frozenID, ra.Source)
	assert.Equal(t, cashID, ra.Destination)
}

func TestResolvedAccounts_FreezeConfiscate(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	frozenID, confiscatedID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeFrozen).Return(frozenID, nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeConfiscated, "", "IDR").Return(confiscatedID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), frozenID).Return("IDR", nil)

	p := NewFreezeConfiscate(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New()})

	require.NoError(t, err)
	assert.Equal(t, frozenID, ra.Source)
	assert.Equal(t, confiscatedID, ra.Destination)
}

func TestResolvedAccounts_AdjustmentCredit(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	adjID, cashID := uuid.New(), uuid.New()
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeAdjustment, "", "IDR").Return(adjID, nil)
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("IDR", nil)

	p := NewAdjustmentCredit(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New()})

	require.NoError(t, err)
	assert.Equal(t, adjID, ra.Source)
	assert.Equal(t, cashID, ra.Destination)
}

func TestResolvedAccounts_AdjustmentDebit(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	cashID, adjID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeAdjustment, "", "IDR").Return(adjID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("IDR", nil)

	p := NewAdjustmentDebit(repo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New()})

	require.NoError(t, err)
	assert.Equal(t, cashID, ra.Source)
	assert.Equal(t, adjID, ra.Destination)
}

func TestResolvedAccounts_FxOut(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	cashID, fxID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("IDR", nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeFxConversion, "IDRUSD", "IDR").Return(fxID, nil)

	p := NewFxOut(repo)
	ra, currency, err := p.ResolveAccounts(context.Background(), Command{
		UserID:   uuid.New(),
		Metadata: map[string]any{"quote_id": "q1", "rate": "15800", "pair": "IDRUSD"},
	})

	require.NoError(t, err)
	assert.Equal(t, cashID, ra.Source)
	assert.Equal(t, fxID, ra.Destination)
	assert.Equal(t, "IDR", currency)
}

func TestResolvedAccounts_FxIn(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	cashID, fxID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("USD", nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeFxConversion, "IDRUSD", "USD").Return(fxID, nil)

	p := NewFxIn(repo)
	ra, currency, err := p.ResolveAccounts(context.Background(), Command{
		UserID:   uuid.New(),
		Metadata: map[string]any{"quote_id": "q1", "rate": "15800", "pair": "IDRUSD"},
	})

	require.NoError(t, err)
	assert.Equal(t, fxID, ra.Source)
	assert.Equal(t, cashID, ra.Destination)
	assert.Equal(t, "USD", currency)
}

func TestResolvedAccounts_Disbursement(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	cashID, settlementID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountID(gomock.Any(), gomock.Any(), constant.AccountTypeCash).Return(cashID, nil)
	repo.EXPECT().GetAccountCurrency(gomock.Any(), cashID).Return("IDR", nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeSettlement, "platform", "IDR").Return(settlementID, nil)

	p := NewDisbursement(repo)
	ra, currency, err := p.ResolveAccounts(context.Background(), Command{UserID: uuid.New()})

	require.NoError(t, err)
	assert.Equal(t, settlementID, ra.Source)
	assert.Equal(t, cashID, ra.Destination)
	assert.Equal(t, "IDR", currency)
}

func TestResolvedAccounts_InterestAccrue(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()
	savingsID, expenseID := uuid.New(), uuid.New()
	repo.EXPECT().GetAccountCurrency(gomock.Any(), savingsID).Return("IDR", nil)
	repo.EXPECT().GetSystemAccountID(gomock.Any(), constant.AccountTypeInterestExpense, "", "IDR").Return(expenseID, nil)

	p := NewInterestAccrue(repo)
	ra, currency, err := p.ResolveAccounts(context.Background(), Command{
		Metadata: map[string]any{"account_id": savingsID.String(), "accrual_date": "2026-07-12", "rate_bps": "500"},
	})

	require.NoError(t, err)
	assert.Equal(t, expenseID, ra.Source)
	assert.Equal(t, savingsID, ra.Destination)
	assert.Equal(t, "IDR", currency)
}

func TestResolvedAccounts_Reversal_SourceDestinationAlwaysNil(t *testing.T) {
	// Reversal can touch more than two accounts (e.g. reversing a
	// transaction with a fee leg) — decision K2, docs/roadmap/archive/13: no single
	// semantic source->destination pair, so both stay uuid.Nil even though
	// Ordered is populated and non-empty.
	txRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	accRepo, ctrl2 := newMockAccountRepo(t)
	defer ctrl2.Finish()

	refID := uuid.New()
	id1, id2 := uuid.New(), uuid.New()
	txRepo.EXPECT().GetAccountIDs(gomock.Any(), refID).Return([]uuid.UUID{id1, id2}, nil)
	accRepo.EXPECT().GetAccountCurrency(gomock.Any(), id1).Return("IDR", nil)

	p := NewReversal(txRepo, accRepo)
	ra, _, err := p.ResolveAccounts(context.Background(), Command{ReferenceID: refID})

	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, ra.Source)
	assert.Equal(t, uuid.Nil, ra.Destination)
	assert.ElementsMatch(t, []uuid.UUID{id1, id2}, ra.Ordered)
}
