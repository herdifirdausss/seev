package processors

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
)

// =============================================================================
// 8. WithdrawPendingCancel — user.pending → user.cash (manual confirm: rejected)
//
// ReferenceID (required) must point at the withdraw_pending this cancels
// (docs/plan/14 Task T2) — full amount only, no partial cancel in MVP.
// =============================================================================

type WithdrawPendingCancel struct {
	repo   repository.AccountRepository
	txRepo repository.TransactionRepository
}

func NewWithdrawPendingCancel(r repository.AccountRepository, txRepo repository.TransactionRepository) *WithdrawPendingCancel {
	return &WithdrawPendingCancel{repo: r, txRepo: txRepo}
}
func (p *WithdrawPendingCancel) Type() string { return "withdraw_pending_cancel" }

func (p *WithdrawPendingCancel) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	pendingID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypePending)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_pending_cancel: pending: %w", err)
	}
	cashID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_pending_cancel: cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, pendingID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_pending_cancel: currency: %w", err)
	}
	return twoLeg(pendingID, cashID), currency, nil
}

func (p *WithdrawPendingCancel) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if err := validateOriginalForClose(ctx, tx, p.txRepo, cmd.ReferenceID, "withdraw_pending", cmd.Amount); err != nil {
		return err
	}
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *WithdrawPendingCancel) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: "pending withdraw cancelled (manual)"},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *WithdrawPendingCancel) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *WithdrawPendingCancel) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand requires ReferenceID (docs/plan/14 Task T2) — the
// withdraw_pending this cancel is closing.
func (p *WithdrawPendingCancel) ValidateCommand(_ context.Context, cmd Command) error {
	return requireReferenceID(cmd, "withdraw_pending_cancel")
}
