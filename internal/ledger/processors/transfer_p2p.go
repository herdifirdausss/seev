package processors

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
)

// =============================================================================
// 9. TransferP2P — sender.cash → receiver.cash
//
// Metadata:
//   "fee_amount"  (optional) — transfer fee charged to sender
//   "fee_gateway" (optional, required if fee_amount set) — typically "platform"
//
// NOTE: No amount caps — enforce at API/policy layer.
//
// [0] = sender.cash
// [1] = receiver.cash
// [2] = fee[fee_gateway]  (only when fee_amount > 0)
// =============================================================================

type TransferP2P struct{ repo repository.AccountRepository }

func NewTransferP2P(r repository.AccountRepository) *TransferP2P { return &TransferP2P{repo: r} }
func (p *TransferP2P) Type() string                              { return "transfer_p2p" }

func (p *TransferP2P) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	if cmd.TargetUserID == uuid.Nil {
		return ResolvedAccounts{}, "", fmt.Errorf("transfer_p2p: TargetUserID required")
	}
	if cmd.UserID == cmd.TargetUserID {
		return ResolvedAccounts{}, "", apperror.NewBizErr(apperror.ErrSelfTransfer, "cannot transfer to yourself")
	}
	senderID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("transfer_p2p: sender cash: %w", err)
	}
	receiverID, err := p.repo.GetAccountID(ctx, cmd.TargetUserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("transfer_p2p: receiver cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, senderID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("transfer_p2p: currency: %w", err)
	}
	resolved := twoLeg(senderID, receiverID)
	if feeID, _, err2 := resolveInlineFee(ctx, p.repo, cmd, currency); err2 != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("transfer_p2p: %w", err2)
	} else if feeID != uuid.Nil {
		resolved.Ordered = append(resolved.Ordered, feeID)
	}
	return resolved, currency, nil
}

func (p *TransferP2P) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	v := MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
		NotSelfTransferValidator{A: cmd.AccountIDs[0], B: cmd.AccountIDs[1]},
	}
	if _, _, ok := hasFee(cmd); ok {
		v = append(v, FeeAmountValidator{})
	}
	return v.Validate(ctx, tx, cmd, bal)
}

func (p *TransferP2P) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	feeID, fee, withFee := hasFee(cmd)
	net := cmd.Amount
	if withFee {
		net = cmd.Amount.Sub(fee)
	}
	entries := []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: net},
	}
	if withFee {
		entries = append(entries, model.EntryInstruction{
			AccountID: feeID, Direction: constant.Credit, Amount: fee, Note: "p2p transfer fee",
		})
	}
	return entries, nil
}

func (p *TransferP2P) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *TransferP2P) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *TransferP2P) ValidateCommand(_ context.Context, _ Command) error { return nil }
