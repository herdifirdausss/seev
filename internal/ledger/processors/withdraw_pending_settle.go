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
// 7. WithdrawPendingSettle — user.pending → settlement[gateway] (manual confirm: disbursed)
//
// Metadata: "gateway" (required) — same rail as the original withdraw_initiate.
// ReferenceID (required) must point at the withdraw_pending this settles
// (docs/roadmap/archive/14 Task T2) — full amount only, no partial settle in MVP.
// =============================================================================

type WithdrawPendingSettle struct {
	repo   repository.AccountRepository
	txRepo repository.TransactionRepository
}

func NewWithdrawPendingSettle(r repository.AccountRepository, txRepo repository.TransactionRepository) *WithdrawPendingSettle {
	return &WithdrawPendingSettle{repo: r, txRepo: txRepo}
}
func (p *WithdrawPendingSettle) Type() string { return "withdraw_pending_settle" }

func (p *WithdrawPendingSettle) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	gateway, err := requireGateway(cmd, "withdraw_pending_settle")
	if err != nil {
		return ResolvedAccounts{}, "", err
	}
	pendingID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypePending)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_pending_settle: pending: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, pendingID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_pending_settle: currency: %w", err)
	}
	sysID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, gateway, currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_pending_settle: settlement[%s]: %w", gateway, err)
	}
	return twoLeg(pendingID, sysID), currency, nil
}

func (p *WithdrawPendingSettle) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if err := validateOriginalForClose(ctx, tx, p.txRepo, cmd.ReferenceID, "withdraw_pending", cmd.Amount); err != nil {
		return err
	}
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *WithdrawPendingSettle) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: "pending withdraw settled (manual)"},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *WithdrawPendingSettle) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *WithdrawPendingSettle) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand requires ReferenceID (docs/roadmap/archive/14 Task T2) — the
// withdraw_pending this settle is closing.
func (p *WithdrawPendingSettle) ValidateCommand(_ context.Context, cmd Command) error {
	return requireReferenceID(cmd, "withdraw_pending_settle")
}
