package processors

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/constants"
	"github.com/herdifirdausss/seev/services/ledger/internal/metadata"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
)

// InterestAdjustment posts an explicit, linked correction without mutating a
// closed period. Accrual corrections use expense/payable legs; capitalization
// corrections use payable/user-account legs. Negative corrections reverse the
// selected stage and use the same account-safety checks as any user debit.
type InterestAdjustment struct{ repo repository.AccountRepository }

func NewInterestAdjustment(r repository.AccountRepository) *InterestAdjustment {
	return &InterestAdjustment{repo: r}
}

func (p *InterestAdjustment) Type() string { return "interest_adjustment" }

func (p *InterestAdjustment) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	accountID, err := metadata.MetaUUID(cmd.Metadata, "account_id")
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_adjustment: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, accountID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_adjustment: currency: %w", err)
	}
	expenseID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeInterestExpense, "", currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_adjustment: interest expense: %w", err)
	}
	payableID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeAccruedInterestPayable, "", currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_adjustment: payable: %w", err)
	}
	stage, err := metadata.MetaString(cmd.Metadata, "correction_stage")
	if err != nil || (stage != "accrual" && stage != "capitalization") {
		return ResolvedAccounts{}, "", fmt.Errorf("%w: interest_adjustment correction_stage must be accrual or capitalization", apperror.ErrValidation)
	}
	direction, _ := metadata.MetaString(cmd.Metadata, "direction")
	if stage == "accrual" {
		if direction == "negative" {
			return twoLeg(payableID, expenseID), currency, nil
		}
		return twoLeg(expenseID, payableID), currency, nil
	}
	if direction == "negative" {
		return twoLeg(accountID, payableID), currency, nil
	}
	return twoLeg(payableID, accountID), currency, nil
}

func (p *InterestAdjustment) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	validators := MultiValidator{PositiveAmountValidator{}, IntegralAmountValidator{}}
	direction, _ := metadata.MetaString(cmd.Metadata, "direction")
	if direction == "negative" {
		validators = append(validators, SufficientFundsValidator{AccountID: cmd.AccountIDs[0]})
	}
	return validators.Validate(ctx, tx, cmd, bal)
}

func (p *InterestAdjustment) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	adjustmentID, _ := metadata.MetaString(cmd.Metadata, "adjustment_id")
	direction, _ := metadata.MetaString(cmd.Metadata, "direction")
	note := fmt.Sprintf("interest adjustment id=%s direction=%s", adjustmentID, direction)
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: note},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount, Note: "explicit interest correction"},
	}, nil
}

func (p *InterestAdjustment) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}

func (p *InterestAdjustment) AfterCommit(_ context.Context, _ Command) error { return nil }

func (p *InterestAdjustment) ValidateCommand(_ context.Context, cmd Command) error {
	if _, err := metadata.MetaUUID(cmd.Metadata, "account_id"); err != nil {
		return fmt.Errorf("%w: interest_adjustment requires account_id", apperror.ErrValidation)
	}
	for _, key := range []string{"adjustment_id", "period_id", "enrollment_id"} {
		if value, err := metadata.MetaString(cmd.Metadata, key); err != nil || value == "" {
			return fmt.Errorf("%w: interest_adjustment requires %s", apperror.ErrValidation, key)
		}
	}
	stage, err := metadata.MetaString(cmd.Metadata, "correction_stage")
	if err != nil || (stage != "accrual" && stage != "capitalization") {
		return fmt.Errorf("%w: interest_adjustment correction_stage must be accrual or capitalization", apperror.ErrValidation)
	}
	direction, err := metadata.MetaString(cmd.Metadata, "direction")
	if err != nil || (direction != "positive" && direction != "negative") {
		return fmt.Errorf("%w: interest_adjustment direction must be positive or negative", apperror.ErrValidation)
	}
	return nil
}
