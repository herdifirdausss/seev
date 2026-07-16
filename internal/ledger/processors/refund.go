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
// 11. Refund — merchant.settlement → user.cash
// Metadata: "merchant_account_id" (UUID string)
// =============================================================================

type Refund struct{ repo repository.AccountRepository }

func NewRefund(r repository.AccountRepository) *Refund { return &Refund{repo: r} }
func (p *Refund) Type() string                         { return "refund" }

func (p *Refund) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	if cmd.TargetUserID == uuid.Nil {
		return ResolvedAccounts{}, "", fmt.Errorf("refund: TargetUserID (refund receiver) required")
	}
	merchantID, err := generalutil.MetaUUID(cmd.Metadata, "merchant_account_id")
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("refund: %w", err)
	}
	userCashID, err := p.repo.GetAccountID(ctx, cmd.TargetUserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("refund: user cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, userCashID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("refund: currency: %w", err)
	}
	return twoLeg(merchantID, userCashID), currency, nil
}

func (p *Refund) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *Refund) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: "refund to user"},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *Refund) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *Refund) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *Refund) ValidateCommand(_ context.Context, _ Command) error { return nil }
