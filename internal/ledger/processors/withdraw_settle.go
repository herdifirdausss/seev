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
// 5. WithdrawSettle — user.hold → settlement[gateway] (disbursed successfully)
//
// Metadata: "gateway" (required) — the bank rail used for disbursement.
// Optional inline fee via "fee_amount"/"fee_gateway" (docs/plan/25 Task T2):
// the withdraw fee is charged HERE, on settle, never on withdraw_initiate —
// validateOriginalForClose demands the exact original amount at close and
// withdraw_cancel refunds the full hold, so an initiate-time fee would
// either break the close or strand the fee on a cancelled withdrawal. With
// a fee the entries become [hold debit amount, settlement credit amount−fee,
// fee[fee_gateway] credit fee] — deduct-from-amount semantics like every
// other inline fee: the user receives amount−fee at the bank rail, and a
// CANCELLED withdrawal pays zero fee. Mirrors escrow_release exactly (the
// established validateOriginalForClose + inline-fee combination).
// ReferenceID (required) must point at the withdraw_initiate this settles
// (docs/plan/14 Task T2) — full amount only, no partial settle in MVP.
// =============================================================================

type WithdrawSettle struct {
	repo   repository.AccountRepository
	txRepo repository.TransactionRepository
}

func NewWithdrawSettle(r repository.AccountRepository, txRepo repository.TransactionRepository) *WithdrawSettle {
	return &WithdrawSettle{repo: r, txRepo: txRepo}
}
func (p *WithdrawSettle) Type() string { return "withdraw_settle" }

func (p *WithdrawSettle) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	gateway, err := requireGateway(cmd, "withdraw_settle")
	if err != nil {
		return ResolvedAccounts{}, "", err
	}
	holdID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeHold)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_settle: hold: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, holdID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_settle: currency: %w", err)
	}
	sysID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, gateway, currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_settle: settlement[%s]: %w", gateway, err)
	}
	resolved := twoLeg(holdID, sysID)
	if feeID, _, err2 := resolveInlineFee(ctx, p.repo, cmd, currency); err2 != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_settle: %w", err2)
	} else if feeID != uuid.Nil {
		resolved.Ordered = append(resolved.Ordered, feeID)
	}
	return resolved, currency, nil
}

func (p *WithdrawSettle) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if err := validateOriginalForClose(ctx, tx, p.txRepo, cmd.ReferenceID, "withdraw_initiate", cmd.Amount); err != nil {
		return err
	}
	v := MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}
	if _, _, ok := hasFee(cmd); ok {
		v = append(v, FeeAmountValidator{})
	}
	return v.Validate(ctx, tx, cmd, bal)
}

func (p *WithdrawSettle) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	feeID, fee, withFee := hasFee(cmd)
	net := cmd.Amount
	if withFee {
		net = cmd.Amount.Sub(fee)
	}
	entries := []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: "withdraw settled"},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: net},
	}
	if withFee {
		entries = append(entries, model.EntryInstruction{
			AccountID: feeID, Direction: constant.Credit, Amount: fee, Note: "withdraw fee",
		})
	}
	return entries, nil
}

func (p *WithdrawSettle) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *WithdrawSettle) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand requires ReferenceID (docs/plan/14 Task T2) — the
// withdraw_initiate this settle is closing.
func (p *WithdrawSettle) ValidateCommand(_ context.Context, cmd Command) error {
	return requireReferenceID(cmd, "withdraw_settle")
}
