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
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// =============================================================================
// 12. FeeCollect — user.cash → fee[gateway]
//
// SCOPE: Standalone fees ONLY — subscription billing, late payment penalty,
//   account maintenance fee, manual fee assessment. Any fee that is NOT
//   associated with a concurrent money movement.
//
//   For fees that accompany a movement (MDR, withdrawal fee, transfer fee,
//   marketplace commission), use the inline fee on the respective processor
//   instead. This keeps the fee atomic with the movement in one DB tx.
//
//   FeeCollect is still valuable for:
//     - Monthly subscription: debit user, credit fee["platform"]
//     - Penalty fee: debit user, credit fee["platform"]
//     - Inactivity fee: scheduled job triggers this processor
//     - Any fee where there is no concurrent debit/credit elsewhere
//
// Fee account sharded by gateway — same rationale as inline fee:
//   fee["platform"] for internal fees, fee["bca"] for BCA-specific charges.
//
// Metadata:
//   "fee_gateway"  (required) — which fee[gateway] account to credit
//   "reason"       (optional) — "subscription", "penalty", "maintenance", etc.
//
// NOTE: No merchant split here. If you need user → merchant + platform fee
//   in one tx, compose money_out (with inline fee) or use a custom processor.
//
// [0] = user.cash
// [1] = fee[gateway]
// =============================================================================

type FeeCollect struct{ repo repository.AccountRepository }

func NewFeeCollect(r repository.AccountRepository) *FeeCollect { return &FeeCollect{repo: r} }
func (p *FeeCollect) Type() string                             { return "fee_collect" }

func (p *FeeCollect) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	feeGateway, err := generalutil.MetaString(cmd.Metadata, "fee_gateway")
	if err != nil || feeGateway == "" {
		return ResolvedAccounts{}, "", fmt.Errorf("%w: fee_collect requires metadata 'fee_gateway' (e.g. 'platform', 'bca')", apperror.ErrValidation)
	}
	userCashID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("fee_collect: user cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, userCashID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("fee_collect: currency: %w", err)
	}
	feeAccID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeFee, feeGateway, currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("fee_collect: fee[%s]: %w", feeGateway, err)
	}
	return twoLeg(userCashID, feeAccID), currency, nil
}

func (p *FeeCollect) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *FeeCollect) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	reason, _ := generalutil.MetaString(cmd.Metadata, "reason")
	note := "fee_collect"
	if reason != "" {
		note += ": " + reason
	}
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: note},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *FeeCollect) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *FeeCollect) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *FeeCollect) ValidateCommand(_ context.Context, _ Command) error { return nil }
