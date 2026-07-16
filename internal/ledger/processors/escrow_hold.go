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
// 14. EscrowHold — buyer.cash → escrow[currency]
//
// Escrow sharded by currency to isolate FX balances and simplify reconciliation.
//
// Metadata:
//   "fee_amount"  (optional) — upfront booking or platform fee charged to buyer
//   "fee_gateway" (optional, required if fee_amount set) — typically "platform"
//
// Most marketplaces charge commission at escrow_release, not here. Only use
// the inline fee if your model requires a non-refundable upfront booking charge.
// If the fee should be refunded on cancellation, do NOT use inline fee here
// — use escrow_release fee instead, or a separate fee_collect at release time.
//
// [0] = buyer.cash
// [1] = escrow[currency]
// [2] = fee[fee_gateway]  (only when fee_amount > 0)
// =============================================================================

type EscrowHold struct{ repo repository.AccountRepository }

func NewEscrowHold(r repository.AccountRepository) *EscrowHold { return &EscrowHold{repo: r} }
func (p *EscrowHold) Type() string                             { return "escrow_hold" }

func (p *EscrowHold) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	cashID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("escrow_hold: buyer cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, cashID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("escrow_hold: currency: %w", err)
	}
	// Escrow's shard key IS currency (see processors.go's sharding table), so
	// qualifier and the currency filter are the same value here — not a typo.
	escrowID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeEscrow, currency, currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("escrow_hold: escrow[%s]: %w", currency, err)
	}
	resolved := twoLeg(cashID, escrowID)
	if feeID, _, err2 := resolveInlineFee(ctx, p.repo, cmd, currency); err2 != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("escrow_hold: %w", err2)
	} else if feeID != uuid.Nil {
		resolved.Ordered = append(resolved.Ordered, feeID)
	}
	return resolved, currency, nil
}

func (p *EscrowHold) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	v := MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}
	if _, _, ok := hasFee(cmd); ok {
		v = append(v, FeeAmountValidator{})
	}
	return v.Validate(ctx, tx, cmd, bal)
}

func (p *EscrowHold) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	feeID, fee, withFee := hasFee(cmd)
	holdAmount := cmd.Amount
	if withFee {
		holdAmount = cmd.Amount.Sub(fee)
	}
	entries := []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: "escrow hold"},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: holdAmount},
	}
	if withFee {
		entries = append(entries, model.EntryInstruction{
			AccountID: feeID, Direction: constant.Credit, Amount: fee, Note: "escrow booking fee",
		})
	}
	return entries, nil
}

func (p *EscrowHold) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *EscrowHold) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *EscrowHold) ValidateCommand(_ context.Context, _ Command) error { return nil }
