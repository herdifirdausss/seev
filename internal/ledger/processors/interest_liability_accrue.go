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

// InterestLiabilityAccrue recognises interest expense and the corresponding
// accrued-interest payable liability. It intentionally does not credit the
// user account; that is the separate monthly capitalization transaction.
type InterestLiabilityAccrue struct{ repo repository.AccountRepository }

func NewInterestLiabilityAccrue(r repository.AccountRepository) *InterestLiabilityAccrue {
	return &InterestLiabilityAccrue{repo: r}
}

func (p *InterestLiabilityAccrue) Type() string { return "interest_liability_accrue" }

func (p *InterestLiabilityAccrue) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	accountID, err := generalutil.MetaUUID(cmd.Metadata, "account_id")
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_liability_accrue: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, accountID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_liability_accrue: currency: %w", err)
	}
	expenseID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeInterestExpense, "", currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_liability_accrue: interest expense: %w", err)
	}
	payableID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeAccruedInterestPayable, "", currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_liability_accrue: payable: %w", err)
	}
	return ResolvedAccounts{Ordered: []uuid.UUID{expenseID, payableID}, Source: expenseID, Destination: payableID}, currency, nil
}

func (p *InterestLiabilityAccrue) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	return MultiValidator{PositiveAmountValidator{}, IntegralAmountValidator{}}.Validate(ctx, tx, cmd, bal)
}

func (p *InterestLiabilityAccrue) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	date, _ := generalutil.MetaString(cmd.Metadata, "accrual_date")
	rate, _ := generalutil.MetaString(cmd.Metadata, "rate_bps")
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: fmt.Sprintf("interest liability accrual date=%s rate_bps=%s", date, rate)},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount, Note: "accrued interest payable"},
	}, nil
}

func (p *InterestLiabilityAccrue) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *InterestLiabilityAccrue) AfterCommit(_ context.Context, _ Command) error { return nil }

func (p *InterestLiabilityAccrue) ValidateCommand(_ context.Context, cmd Command) error {
	if _, err := generalutil.MetaUUID(cmd.Metadata, "account_id"); err != nil {
		return fmt.Errorf("%w: interest_liability_accrue requires account_id", apperror.ErrValidation)
	}
	for _, key := range []string{"accrual_date", "rate_bps", "enrollment_id", "period_id"} {
		if value, err := generalutil.MetaString(cmd.Metadata, key); err != nil || value == "" {
			return fmt.Errorf("%w: interest_liability_accrue requires %s", apperror.ErrValidation, key)
		}
	}
	return nil
}

// InterestCapitalize releases the accrued-interest payable liability into the
// user's savings account. The source is a currency-sharded system account;
// the target is identified by an internal enrollment/account reference.
type InterestCapitalize struct{ repo repository.AccountRepository }

func NewInterestCapitalize(r repository.AccountRepository) *InterestCapitalize {
	return &InterestCapitalize{repo: r}
}

func (p *InterestCapitalize) Type() string { return "interest_capitalize" }

func (p *InterestCapitalize) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	accountID, err := generalutil.MetaUUID(cmd.Metadata, "account_id")
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_capitalize: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, accountID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_capitalize: currency: %w", err)
	}
	payableID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeAccruedInterestPayable, "", currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_capitalize: payable: %w", err)
	}
	return twoLeg(payableID, accountID), currency, nil
}

func (p *InterestCapitalize) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	return MultiValidator{PositiveAmountValidator{}, IntegralAmountValidator{}}.Validate(ctx, tx, cmd, bal)
}

func (p *InterestCapitalize) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	period, _ := generalutil.MetaString(cmd.Metadata, "period_id")
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: "release accrued interest payable period=" + period},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount, Note: "monthly interest capitalization"},
	}, nil
}

func (p *InterestCapitalize) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *InterestCapitalize) AfterCommit(_ context.Context, _ Command) error { return nil }

func (p *InterestCapitalize) ValidateCommand(_ context.Context, cmd Command) error {
	if _, err := generalutil.MetaUUID(cmd.Metadata, "account_id"); err != nil {
		return fmt.Errorf("%w: interest_capitalize requires account_id", apperror.ErrValidation)
	}
	for _, key := range []string{"period_id", "enrollment_id"} {
		if value, err := generalutil.MetaString(cmd.Metadata, key); err != nil || value == "" {
			return fmt.Errorf("%w: interest_capitalize requires %s", apperror.ErrValidation, key)
		}
	}
	return nil
}
