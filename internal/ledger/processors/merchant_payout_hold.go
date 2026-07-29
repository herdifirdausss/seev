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
// 32. MerchantPayoutHold — merchant.cash → merchant.hold
//
// Plan 57 T6 — the merchant-owned counterpart of WithdrawInitiate. A
// merchant tenant needs its own hold account for the payout state machine
// (create → held → submitted → vendor_pending → settled|cancelled) to work
// identically to the existing user path; provisioned via
// provision.Service.ProvisionMerchantAccount(..., constant.AccountTypeHold)
// the same idempotent upsert T5 already built, just a different account
// type.
//
// [0] = merchant.cash
// [1] = merchant.hold
// [2] = fee[fee_gateway]  (only when fee_amount > 0)
//
// Internal-router-only — never added to publicUserTypes.
// =============================================================================

type MerchantPayoutHold struct{ repo repository.AccountRepository }

func NewMerchantPayoutHold(r repository.AccountRepository) *MerchantPayoutHold {
	return &MerchantPayoutHold{repo: r}
}
func (p *MerchantPayoutHold) Type() string { return "merchant_payout_hold" }

func (p *MerchantPayoutHold) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	if cmd.MerchantTenantID == uuid.Nil {
		return ResolvedAccounts{}, "", fmt.Errorf("%w: merchant_payout_hold requires MerchantTenantID", apperror.ErrValidation)
	}
	srcID, err := p.repo.GetMerchantAccountID(ctx, cmd.MerchantTenantID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payout_hold: source: %w", err)
	}
	holdID, err := p.repo.GetMerchantAccountID(ctx, cmd.MerchantTenantID, constant.AccountTypeHold)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payout_hold: hold: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, srcID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payout_hold: currency: %w", err)
	}
	resolved := twoLeg(srcID, holdID)
	if feeID, _, err2 := resolveInlineFee(ctx, p.repo, cmd, currency); err2 != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payout_hold: %w", err2)
	} else if feeID != uuid.Nil {
		resolved.Ordered = append(resolved.Ordered, feeID)
	}
	return resolved, currency, nil
}

func (p *MerchantPayoutHold) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	v := MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}
	if _, _, ok := hasFee(cmd); ok {
		v = append(v, FeeAmountValidator{})
	}
	return v.Validate(ctx, tx, cmd, bal)
}

func (p *MerchantPayoutHold) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
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
			AccountID: feeID, Direction: constant.Credit, Amount: fee, Note: "merchant payout initiation fee",
		})
	}
	return entries, nil
}

func (p *MerchantPayoutHold) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *MerchantPayoutHold) AfterCommit(_ context.Context, _ Command) error { return nil }

func (p *MerchantPayoutHold) ValidateCommand(_ context.Context, cmd Command) error {
	if cmd.MerchantTenantID == uuid.Nil {
		return fmt.Errorf("%w: merchant_payout_hold requires MerchantTenantID", apperror.ErrValidation)
	}
	return nil
}
