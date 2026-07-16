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
// 3. WithdrawInitiate — user.cash|pocket → user.hold
//
// Metadata:
//   "fee_amount"  (optional) — flat withdrawal fee charged at initiation
//   "fee_gateway" (optional, required if fee_amount set)
//
// Some platforms charge the withdrawal fee here (upfront at lock time).
// Others charge it at withdraw_settle (at disbursement time). Pick one
// and enforce the convention in your orchestration layer — don't do both.
//
// [0] = user.cash | user.pocket
// [1] = user.hold
// [2] = fee[fee_gateway]  (only when fee_amount > 0)
// =============================================================================

type WithdrawInitiate struct{ repo repository.AccountRepository }

func NewWithdrawInitiate(r repository.AccountRepository) *WithdrawInitiate {
	return &WithdrawInitiate{repo: r}
}
func (p *WithdrawInitiate) Type() string { return "withdraw_initiate" }

func (p *WithdrawInitiate) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	var srcID uuid.UUID
	var err error
	if cmd.PocketCode != "" {
		srcID, err = p.repo.GetPocketAccountID(ctx, cmd.UserID, cmd.PocketCode)
	} else {
		srcID, err = p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	}
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_initiate: source: %w", err)
	}
	holdID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeHold)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_initiate: hold: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, srcID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_initiate: currency: %w", err)
	}
	resolved := twoLeg(srcID, holdID)
	if feeID, _, err2 := resolveInlineFee(ctx, p.repo, cmd, currency); err2 != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("withdraw_initiate: %w", err2)
	} else if feeID != uuid.Nil {
		resolved.Ordered = append(resolved.Ordered, feeID)
	}
	return resolved, currency, nil
}

func (p *WithdrawInitiate) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	v := MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}
	if _, _, ok := hasFee(cmd); ok {
		v = append(v, FeeAmountValidator{})
	}
	return v.Validate(ctx, tx, cmd, bal)
}

func (p *WithdrawInitiate) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	feeID, fee, withFee := hasFee(cmd)
	holdAmount := cmd.Amount
	if withFee {
		holdAmount = cmd.Amount.Sub(fee)
	}
	entries := []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: holdAmount},
	}
	if withFee {
		entries = append(entries, model.EntryInstruction{
			AccountID: feeID, Direction: constant.Credit, Amount: fee, Note: "withdraw initiation fee",
		})
	}
	return entries, nil
}

func (p *WithdrawInitiate) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *WithdrawInitiate) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *WithdrawInitiate) ValidateCommand(_ context.Context, _ Command) error { return nil }
