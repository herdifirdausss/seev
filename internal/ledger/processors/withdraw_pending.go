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
// 4. WithdrawPending — user.hold → user.pending
//
// Triggered by a background job when a withdraw_initiate has not settled
// within the SLA threshold (e.g. 1 hour). Moves funds to a distinct
// "pending" account to signal manual bank reconciliation is required.
//
// Lifecycle after this:
//   Bank confirmed  → withdraw_pending_settle  (pending → settlement)
//   Bank rejected   → withdraw_pending_cancel  (pending → cash)
// =============================================================================

type WithdrawPending struct{ repo repository.AccountRepository }

func NewWithdrawPending(r repository.AccountRepository) *WithdrawPending {
	return &WithdrawPending{repo: r}
}
func (p *WithdrawPending) Type() string { return "withdraw_pending" }

func (p *WithdrawPending) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	holdID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeHold)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_pending: hold: %w", err)
	}
	pendingID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypePending)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_pending: pending: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, holdID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_pending: currency: %w", err)
	}
	return twoLeg(holdID, pendingID), currency, nil
}

func (p *WithdrawPending) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *WithdrawPending) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: "escalate to pending: SLA breach"},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *WithdrawPending) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *WithdrawPending) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *WithdrawPending) ValidateCommand(_ context.Context, _ Command) error { return nil }
