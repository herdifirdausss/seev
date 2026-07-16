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
// 20. AdjustmentCredit — system.adjustment → user.cash
//
// Manual credit by ops/finance: reconciliation error correction, compensation,
// promotional credit. Always requires an authorizer and reason.
//
// Metadata: "reason"       - e.g. "reconciliation", "compensation", "promo"
//           "authorized_by"- employee ID or system process name
//           "ticket_ref"   - internal ops ticket reference
// =============================================================================

type AdjustmentCredit struct{ repo repository.AccountRepository }

func NewAdjustmentCredit(r repository.AccountRepository) *AdjustmentCredit {
	return &AdjustmentCredit{repo: r}
}
func (p *AdjustmentCredit) Type() string { return "adjustment_credit" }

func (p *AdjustmentCredit) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	cashID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("adjustment_credit: user cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, cashID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("adjustment_credit: currency: %w", err)
	}
	adjID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeAdjustment, "", currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("adjustment_credit: adjustment account: %w", err)
	}
	return twoLeg(adjID, cashID), currency, nil
}

func (p *AdjustmentCredit) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if _, err := generalutil.MetaString(cmd.Metadata, "authorized_by"); err != nil {
		return fmt.Errorf("%w: adjustment_credit requires metadata 'authorized_by'", apperror.ErrValidation)
	}
	return MultiValidator{PositiveAmountValidator{}, IntegralAmountValidator{}}.Validate(ctx, tx, cmd, bal)
}

func (p *AdjustmentCredit) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	reason, _ := generalutil.MetaString(cmd.Metadata, "reason")
	ticketRef, _ := generalutil.MetaString(cmd.Metadata, "ticket_ref")
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: fmt.Sprintf("adj credit reason=%s ref=%s", reason, ticketRef)},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *AdjustmentCredit) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *AdjustmentCredit) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *AdjustmentCredit) ValidateCommand(_ context.Context, _ Command) error { return nil }
