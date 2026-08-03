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
// 31. MerchantPayinCredit — settlement[gateway] → merchant.cash
//
// Plan 57 T6 — the merchant-owned counterpart of MoneyIn. Mirrors MoneyIn's
// shape exactly, with the ONE change T5 established: the destination is
// always resolved from cmd.MerchantTenantID via GetMerchantAccountID, never
// from a caller-supplied account id.
//
// Metadata:
//
//	"gateway"     (required) — selects settlement[gateway]
//	"fee_amount"  (optional) — platform MDR/processing fee to retain
//	"fee_gateway" (optional, required if fee_amount set)
//
// [0] = settlement[gateway]
// [1] = merchant.cash
// [2] = fee[fee_gateway]  (only when fee_amount > 0)
//
// Internal-router-only — never added to publicUserTypes. Reachable only
// via internal/payin's own merchant credit path (T6), never directly by a
// B2B caller.
// =============================================================================

type MerchantPayinCredit struct{ repo repository.AccountRepository }

func NewMerchantPayinCredit(r repository.AccountRepository) *MerchantPayinCredit {
	return &MerchantPayinCredit{repo: r}
}
func (p *MerchantPayinCredit) Type() string { return "merchant_payin_credit" }

func (p *MerchantPayinCredit) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	if cmd.MerchantTenantID == uuid.Nil {
		return ResolvedAccounts{}, "", fmt.Errorf("%w: merchant_payin_credit requires MerchantTenantID", apperror.ErrValidation)
	}
	gateway, err := requireGateway(cmd, "merchant_payin_credit")
	if err != nil {
		return ResolvedAccounts{}, "", err
	}
	destID, err := merchantAccountID(ctx, p.repo, cmd.MerchantTenantID, constant.AccountTypeCash, cmd.Currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payin_credit: merchant cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, destID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payin_credit: currency: %w", err)
	}
	sysID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, gateway, currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payin_credit: settlement[%s]: %w", gateway, err)
	}
	resolved := twoLeg(sysID, destID)
	if feeID, _, err2 := resolveInlineFee(ctx, p.repo, cmd, currency); err2 != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payin_credit: %w", err2)
	} else if feeID != uuid.Nil {
		resolved.Ordered = append(resolved.Ordered, feeID)
	}
	return resolved, currency, nil
}

func (p *MerchantPayinCredit) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	v := MultiValidator{PositiveAmountValidator{}, IntegralAmountValidator{}}
	if _, _, ok := hasFee(cmd); ok {
		v = append(v, FeeAmountValidator{})
	}
	return v.Validate(ctx, tx, cmd, bal)
}

func (p *MerchantPayinCredit) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	feeID, fee, withFee := hasFee(cmd)
	net := cmd.Amount
	if withFee {
		net = cmd.Amount.Sub(fee)
	}
	entries := []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: net, Note: "merchant_payin_credit → cash"},
	}
	if withFee {
		entries = append(entries, model.EntryInstruction{
			AccountID: feeID, Direction: constant.Credit, Amount: fee, Note: "merchant payin fee",
		})
	}
	return entries, nil
}

func (p *MerchantPayinCredit) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *MerchantPayinCredit) AfterCommit(_ context.Context, _ Command) error { return nil }

func (p *MerchantPayinCredit) ValidateCommand(_ context.Context, cmd Command) error {
	if cmd.MerchantTenantID == uuid.Nil {
		return fmt.Errorf("%w: merchant_payin_credit requires MerchantTenantID", apperror.ErrValidation)
	}
	return nil
}
