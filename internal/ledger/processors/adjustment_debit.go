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
// 21. AdjustmentDebit — user.cash → system.adjustment
//
// Manual debit by ops/finance: clawback of erroneous credit, regulatory hold.
// Metadata: same as AdjustmentCredit.
// =============================================================================

type AdjustmentDebit struct{ repo repository.AccountRepository }

func NewAdjustmentDebit(r repository.AccountRepository) *AdjustmentDebit {
	return &AdjustmentDebit{repo: r}
}
func (p *AdjustmentDebit) Type() string { return "adjustment_debit" }

func (p *AdjustmentDebit) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	cashID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("adjustment_debit: user cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, cashID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("adjustment_debit: currency: %w", err)
	}
	adjID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeAdjustment, "", currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("adjustment_debit: adjustment account: %w", err)
	}
	return twoLeg(cashID, adjID), currency, nil
}

func (p *AdjustmentDebit) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if _, err := generalutil.MetaString(cmd.Metadata, "authorized_by"); err != nil {
		return fmt.Errorf("%w: adjustment_debit requires metadata 'authorized_by'", apperror.ErrValidation)
	}
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *AdjustmentDebit) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	reason, _ := generalutil.MetaString(cmd.Metadata, "reason")
	ticketRef, _ := generalutil.MetaString(cmd.Metadata, "ticket_ref")
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: fmt.Sprintf("adj debit reason=%s ref=%s", reason, ticketRef)},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *AdjustmentDebit) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *AdjustmentDebit) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *AdjustmentDebit) ValidateCommand(_ context.Context, _ Command) error { return nil }
