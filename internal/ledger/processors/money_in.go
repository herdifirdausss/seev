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
// 1. MoneyIn — Top-up / Deposit
//
// Destination is user.cash by default; set cmd.PocketCode for pocket deposit.
//
// Metadata:
//   "gateway"     (required) — e.g. "bca", "gopay" — selects settlement[gateway]
//   "fee_amount"  (optional) — platform MDR or processing fee to retain
//   "fee_gateway" (optional, required if fee_amount set) — e.g. "bca", "platform"
//
// 2-entry (no fee):  settlement[gateway] →  user.cash|pocket
// 3-entry (with fee): settlement[gateway] → (user.cash|pocket  net)
//                                         → (fee[fee_gateway]  fee)
//
// NOTE: For gateway MDR that the bank deducts before remitting, fee_amount
//   represents the portion retained by the platform, not the bank deduction.
//   Bank deduction is already reflected in the gross amount sent by the gateway.
//
// [0] = settlement[gateway]
// [1] = user.cash | user.pocket
// [2] = fee[fee_gateway]  (only when fee_amount > 0)
// =============================================================================

type MoneyIn struct{ repo repository.AccountRepository }

func NewMoneyIn(r repository.AccountRepository) *MoneyIn { return &MoneyIn{repo: r} }
func (p *MoneyIn) Type() string                          { return "money_in" }

func (p *MoneyIn) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	gateway, err := requireGateway(cmd, "money_in")
	if err != nil {
		return ResolvedAccounts{}, "", err
	}
	var destID uuid.UUID
	if cmd.PocketCode != "" {
		destID, err = p.repo.GetPocketAccountID(ctx, cmd.UserID, cmd.PocketCode)
		if err != nil {
			return ResolvedAccounts{}, "", fmt.Errorf("money_in: pocket %q: %w", cmd.PocketCode, err)
		}
	} else {
		destID, err = p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
		if err != nil {
			return ResolvedAccounts{}, "", fmt.Errorf("money_in: cash account: %w", err)
		}
	}
	currency, err := p.repo.GetAccountCurrency(ctx, destID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("money_in: currency: %w", err)
	}
	sysID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, gateway, currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("money_in: settlement[%s]: %w", gateway, err)
	}
	resolved := twoLeg(sysID, destID)
	if feeID, _, err2 := resolveInlineFee(ctx, p.repo, cmd, currency); err2 != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("money_in: %w", err2)
	} else if feeID != uuid.Nil {
		resolved.Ordered = append(resolved.Ordered, feeID)
	}
	return resolved, currency, nil
}

func (p *MoneyIn) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	v := MultiValidator{PositiveAmountValidator{}, IntegralAmountValidator{}}
	if _, _, ok := hasFee(cmd); ok {
		v = append(v, FeeAmountValidator{})
	}
	return v.Validate(ctx, tx, cmd, bal)
}

func (p *MoneyIn) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	destNote := "cash"
	if cmd.PocketCode != "" {
		destNote = "pocket:" + cmd.PocketCode
	}
	feeID, fee, withFee := hasFee(cmd)
	net := cmd.Amount
	if withFee {
		net = cmd.Amount.Sub(fee)
	}
	entries := []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: net, Note: "money_in → " + destNote},
	}
	if withFee {
		entries = append(entries, model.EntryInstruction{
			AccountID: feeID, Direction: constant.Credit, Amount: fee, Note: "money_in fee",
		})
	}
	return entries, nil
}

func (p *MoneyIn) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *MoneyIn) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *MoneyIn) ValidateCommand(_ context.Context, _ Command) error { return nil }
