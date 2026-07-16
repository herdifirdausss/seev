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
// 18. FreezeRelease — user.frozen → user.cash
//
// Investigation cleared — funds returned to user's spendable balance.
// Metadata: "case_id", "released_by"
// =============================================================================

type FreezeRelease struct{ repo repository.AccountRepository }

func NewFreezeRelease(r repository.AccountRepository) *FreezeRelease { return &FreezeRelease{repo: r} }
func (p *FreezeRelease) Type() string                                { return "freeze_release" }

func (p *FreezeRelease) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	frozenID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeFrozen)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("freeze_release: frozen account: %w", err)
	}
	cashID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("freeze_release: user cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, frozenID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("freeze_release: currency: %w", err)
	}
	return twoLeg(frozenID, cashID), currency, nil
}

func (p *FreezeRelease) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if _, err := generalutil.MetaString(cmd.Metadata, "case_id"); err != nil {
		return fmt.Errorf("%w: freeze_release requires metadata 'case_id'", apperror.ErrValidation)
	}
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *FreezeRelease) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	caseID, _ := generalutil.MetaString(cmd.Metadata, "case_id")
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: fmt.Sprintf("freeze released [%s]", caseID)},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *FreezeRelease) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *FreezeRelease) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *FreezeRelease) ValidateCommand(_ context.Context, _ Command) error { return nil }
