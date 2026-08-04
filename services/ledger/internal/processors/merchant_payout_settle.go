package processors

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/constants"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
)

// =============================================================================
// 33. MerchantPayoutSettle — merchant.hold → settlement[gateway]
//
// Plan 57 T6 — the merchant-owned counterpart of WithdrawSettle. ReferenceID
// (required) must point at the merchant_payout_hold this settles, same
// validateOriginalForClose guard as the user path.
//
// [0] = merchant.hold
// [1] = settlement[gateway]
// [2] = fee[fee_gateway]  (only when fee_amount > 0)
//
// Internal-router-only — never added to publicUserTypes.
// =============================================================================

type MerchantPayoutSettle struct {
	repo   repository.AccountRepository
	txRepo repository.TransactionRepository
}

func NewMerchantPayoutSettle(r repository.AccountRepository, txRepo repository.TransactionRepository) *MerchantPayoutSettle {
	return &MerchantPayoutSettle{repo: r, txRepo: txRepo}
}
func (p *MerchantPayoutSettle) Type() string { return "merchant_payout_settle" }

func (p *MerchantPayoutSettle) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	if cmd.MerchantTenantID == uuid.Nil {
		return ResolvedAccounts{}, "", fmt.Errorf("%w: merchant_payout_settle requires MerchantTenantID", apperror.ErrValidation)
	}
	gateway, err := requireGateway(cmd, "merchant_payout_settle")
	if err != nil {
		return ResolvedAccounts{}, "", err
	}
	holdID, err := merchantAccountID(ctx, p.repo, cmd.MerchantTenantID, constant.AccountTypeHold, cmd.Currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payout_settle: hold: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, holdID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payout_settle: currency: %w", err)
	}
	sysID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, gateway, currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payout_settle: settlement[%s]: %w", gateway, err)
	}
	resolved := twoLeg(holdID, sysID)
	if feeID, _, feeErr := resolveInlineFee(ctx, p.repo, cmd, currency); feeErr != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payout_settle: %w", feeErr)
	} else if feeID != uuid.Nil {
		resolved.Ordered = append(resolved.Ordered, feeID)
	}
	return resolved, currency, nil
}

func (p *MerchantPayoutSettle) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if err := validateOriginalForClose(ctx, tx, p.txRepo, cmd.ReferenceID, "merchant_payout_hold", cmd.Amount); err != nil {
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

func (p *MerchantPayoutSettle) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	feeID, fee, withFee := hasFee(cmd)
	net := cmd.Amount
	if withFee {
		net = cmd.Amount.Sub(fee)
	}
	entries := []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: "merchant payout settled"},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: net},
	}
	if withFee {
		entries = append(entries, model.EntryInstruction{
			AccountID: feeID, Direction: constant.Credit, Amount: fee, Note: "merchant payout fee",
		})
	}
	return entries, nil
}

func (p *MerchantPayoutSettle) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *MerchantPayoutSettle) AfterCommit(_ context.Context, _ Command) error { return nil }

func (p *MerchantPayoutSettle) ValidateCommand(_ context.Context, cmd Command) error {
	if cmd.MerchantTenantID == uuid.Nil {
		return fmt.Errorf("%w: merchant_payout_settle requires MerchantTenantID", apperror.ErrValidation)
	}
	return requireReferenceID(cmd, "merchant_payout_settle")
}
