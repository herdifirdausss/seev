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
// 19. FreezeConfiscate — user.frozen → system.confiscated
//
// Investigation confirmed fraud — funds are permanently transferred to a
// system-controlled confiscated account. This is a terminal state.
// Regulatory/legal reporting obligations should be triggered via outbox event.
//
// Metadata: "case_id", "legal_ref" (court order / regulator reference)
// =============================================================================

type FreezeConfiscate struct{ repo repository.AccountRepository }

func NewFreezeConfiscate(r repository.AccountRepository) *FreezeConfiscate {
	return &FreezeConfiscate{repo: r}
}
func (p *FreezeConfiscate) Type() string { return "freeze_confiscate" }

func (p *FreezeConfiscate) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	frozenID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeFrozen)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("freeze_confiscate: frozen account: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, frozenID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("freeze_confiscate: currency: %w", err)
	}
	confiscatedID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeConfiscated, "", currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("freeze_confiscate: confiscated account: %w", err)
	}
	return twoLeg(frozenID, confiscatedID), currency, nil
}

func (p *FreezeConfiscate) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if _, err := generalutil.MetaString(cmd.Metadata, "case_id"); err != nil {
		return fmt.Errorf("%w: freeze_confiscate requires metadata 'case_id'", apperror.ErrValidation)
	}
	if _, err := generalutil.MetaString(cmd.Metadata, "legal_ref"); err != nil {
		return fmt.Errorf("%w: freeze_confiscate requires metadata 'legal_ref'", apperror.ErrValidation)
	}
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *FreezeConfiscate) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	caseID, _ := generalutil.MetaString(cmd.Metadata, "case_id")
	legalRef, _ := generalutil.MetaString(cmd.Metadata, "legal_ref")
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: fmt.Sprintf("confiscated case=%s legal=%s", caseID, legalRef)},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *FreezeConfiscate) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *FreezeConfiscate) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *FreezeConfiscate) ValidateCommand(_ context.Context, _ Command) error { return nil }
