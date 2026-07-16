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
// 17. FreezeInitiate — user.cash → user.frozen
//
// Used by fraud/AML/compliance systems to lock a user's funds while an
// investigation is in progress. Funds sit in a per-user "frozen" account,
// still attributed to the user on the balance sheet, but inaccessible.
//
// Metadata: "reason"       - short code, e.g. "aml_flag", "fraud_suspected"
//           "case_id"      - internal compliance case reference
//           "initiated_by" - "system" | "compliance_officer" | "regulator"
// =============================================================================

type FreezeInitiate struct{ repo repository.AccountRepository }

func NewFreezeInitiate(r repository.AccountRepository) *FreezeInitiate {
	return &FreezeInitiate{repo: r}
}
func (p *FreezeInitiate) Type() string { return "freeze_initiate" }

func (p *FreezeInitiate) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	cashID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("freeze_initiate: user cash: %w", err)
	}
	frozenID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeFrozen)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("freeze_initiate: frozen account: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, cashID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("freeze_initiate: currency: %w", err)
	}
	return twoLeg(cashID, frozenID), currency, nil
}

func (p *FreezeInitiate) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if _, err := generalutil.MetaString(cmd.Metadata, "case_id"); err != nil {
		return fmt.Errorf("%w: freeze_initiate requires metadata 'case_id'", apperror.ErrValidation)
	}
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *FreezeInitiate) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	caseID, _ := generalutil.MetaString(cmd.Metadata, "case_id")
	reason, _ := generalutil.MetaString(cmd.Metadata, "reason")
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: fmt.Sprintf("freeze: %s [%s]", reason, caseID)},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *FreezeInitiate) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *FreezeInitiate) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *FreezeInitiate) ValidateCommand(_ context.Context, _ Command) error { return nil }
