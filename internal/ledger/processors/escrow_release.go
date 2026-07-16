package processors

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// =============================================================================
// 15. EscrowRelease — escrow[currency] → merchant.cash
//
// This is the PRIMARY place for marketplace commission fee. The fee is deducted
// from the escrow amount before paying the merchant — fully atomic.
//
// Metadata:
//   "merchant_account_id" (required)
//   "fee_amount"          (optional) — marketplace commission / platform take rate
//   "fee_gateway"         (optional, required if fee_amount set) — typically "platform"
//
// 2-entry (no fee):   escrow[currency] → merchant.cash          full amount
// 3-entry (with fee): escrow[currency] → merchant.cash          amount - commission
//                                      → fee[fee_gateway]        commission
//
// Currency derived from merchant account to guarantee correct escrow shard.
//
// [0] = escrow[currency]
// [1] = merchant.cash
// [2] = fee[fee_gateway]  (only when fee_amount > 0)
//
// ReferenceID (required) must point at the escrow_hold this releases
// (docs/plan/14 Task T2) — full amount only (the fee split above happens
// WITHIN that full amount, it isn't an additional partial release).
// =============================================================================

type EscrowRelease struct {
	repo   repository.AccountRepository
	txRepo repository.TransactionRepository
}

func NewEscrowRelease(r repository.AccountRepository, txRepo repository.TransactionRepository) *EscrowRelease {
	return &EscrowRelease{repo: r, txRepo: txRepo}
}
func (p *EscrowRelease) Type() string { return "escrow_release" }

func (p *EscrowRelease) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	merchantID, err := generalutil.MetaUUID(cmd.Metadata, "merchant_account_id")
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("escrow_release: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, merchantID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("escrow_release: merchant currency: %w", err)
	}
	escrowID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeEscrow, currency, currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("escrow_release: escrow[%s]: %w", currency, err)
	}
	resolved := twoLeg(escrowID, merchantID)
	if feeID, _, err2 := resolveInlineFee(ctx, p.repo, cmd, currency); err2 != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("escrow_release: %w", err2)
	} else if feeID != uuid.Nil {
		resolved.Ordered = append(resolved.Ordered, feeID)
	}
	return resolved, currency, nil
}

func (p *EscrowRelease) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if err := validateOriginalForClose(ctx, tx, p.txRepo, cmd.ReferenceID, "escrow_hold", cmd.Amount); err != nil {
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

func (p *EscrowRelease) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	feeID, fee, withFee := hasFee(cmd)
	net := cmd.Amount
	if withFee {
		net = cmd.Amount.Sub(fee)
	}
	entries := []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: "escrow release"},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: net},
	}
	if withFee {
		entries = append(entries, model.EntryInstruction{
			AccountID: feeID, Direction: constant.Credit, Amount: fee, Note: "marketplace commission",
		})
	}
	return entries, nil
}

func (p *EscrowRelease) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *EscrowRelease) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand requires ReferenceID (docs/plan/14 Task T2) — the
// escrow_hold this release is closing.
func (p *EscrowRelease) ValidateCommand(_ context.Context, cmd Command) error {
	return requireReferenceID(cmd, "escrow_release")
}
