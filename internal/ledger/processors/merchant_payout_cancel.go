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
// 34. MerchantPayoutCancel — merchant.hold → merchant.cash
//
// Plan 57 T6 — the merchant-owned counterpart of WithdrawCancel. ReferenceID
// (required) must point at the merchant_payout_hold this cancels.
//
// Internal-router-only — never added to publicUserTypes.
// =============================================================================

type MerchantPayoutCancel struct {
	repo   repository.AccountRepository
	txRepo repository.TransactionRepository
}

func NewMerchantPayoutCancel(r repository.AccountRepository, txRepo repository.TransactionRepository) *MerchantPayoutCancel {
	return &MerchantPayoutCancel{repo: r, txRepo: txRepo}
}
func (p *MerchantPayoutCancel) Type() string { return "merchant_payout_cancel" }

func (p *MerchantPayoutCancel) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	if cmd.MerchantTenantID == uuid.Nil {
		return ResolvedAccounts{}, "", fmt.Errorf("%w: merchant_payout_cancel requires MerchantTenantID", apperror.ErrValidation)
	}
	holdID, err := p.repo.GetMerchantAccountID(ctx, cmd.MerchantTenantID, constant.AccountTypeHold)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payout_cancel: hold: %w", err)
	}
	cashID, err := p.repo.GetMerchantAccountID(ctx, cmd.MerchantTenantID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payout_cancel: cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, holdID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_payout_cancel: currency: %w", err)
	}
	return twoLeg(holdID, cashID), currency, nil
}

func (p *MerchantPayoutCancel) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if err := validateOriginalForClose(ctx, tx, p.txRepo, cmd.ReferenceID, "merchant_payout_hold", cmd.Amount); err != nil {
		return err
	}
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *MerchantPayoutCancel) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: "merchant payout cancelled"},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *MerchantPayoutCancel) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *MerchantPayoutCancel) AfterCommit(_ context.Context, _ Command) error { return nil }

func (p *MerchantPayoutCancel) ValidateCommand(_ context.Context, cmd Command) error {
	if cmd.MerchantTenantID == uuid.Nil {
		return fmt.Errorf("%w: merchant_payout_cancel requires MerchantTenantID", apperror.ErrValidation)
	}
	return requireReferenceID(cmd, "merchant_payout_cancel")
}
