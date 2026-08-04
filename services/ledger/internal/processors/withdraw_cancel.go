package processors

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/constants"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
)

// =============================================================================
// 6. WithdrawCancel — user.hold → user.cash (disbursement failed)
//
// ReferenceID (required) must point at the withdraw_initiate this cancels
// (docs/roadmap/archive/14 Task T2) — full amount only, no partial cancel in MVP.
// =============================================================================

type WithdrawCancel struct {
	repo   repository.AccountRepository
	txRepo repository.TransactionRepository
}

func NewWithdrawCancel(r repository.AccountRepository, txRepo repository.TransactionRepository) *WithdrawCancel {
	return &WithdrawCancel{repo: r, txRepo: txRepo}
}
func (p *WithdrawCancel) Type() string { return "withdraw_cancel" }

func (p *WithdrawCancel) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	holdID, err := userAccountID(ctx, p.repo, cmd.UserID, constant.AccountTypeHold, cmd.Currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_cancel: hold: %w", err)
	}
	cashID, err := userAccountID(ctx, p.repo, cmd.UserID, constant.AccountTypeCash, cmd.Currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_cancel: cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, holdID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_cancel: currency: %w", err)
	}
	return twoLeg(holdID, cashID), currency, nil
}

func (p *WithdrawCancel) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if err := validateOriginalForClose(ctx, tx, p.txRepo, cmd.ReferenceID, "withdraw_initiate", cmd.Amount); err != nil {
		return err
	}
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *WithdrawCancel) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: "withdraw cancelled"},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *WithdrawCancel) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *WithdrawCancel) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand requires ReferenceID (docs/roadmap/archive/14 Task T2) — the
// withdraw_initiate this cancel is closing.
func (p *WithdrawCancel) ValidateCommand(_ context.Context, cmd Command) error {
	return requireReferenceID(cmd, "withdraw_cancel")
}
