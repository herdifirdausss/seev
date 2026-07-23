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
// InterestAccrue — system.interest_expense[currency] -> savings account
// (docs/roadmap/archive/19 Task T3)
//
// Daily interest credit for accounts registered in savings_config (there is
// no product table — ops registers exactly which accounts earn interest;
// no magic pocket_code prefix). The amount is computed by
// internal/ledger/service/accrual BEFORE this processor ever runs, from a
// SNAPSHOT balance (account_balance_snapshots, docs/roadmap/archive/15 Task T1), never
// a live balance — this processor doesn't recompute or re-check anything,
// it just posts the two entries.
//
// Metadata: "account_id"   (required) — the savings account to credit; not
//                            derivable from cmd.UserID alone since the
//                            target may be a pocket, not the default cash
//                            account.
//           "accrual_date" (required) — YYYY-MM-DD, audit trail only.
//           "rate_bps"     (required) — audit trail only, NEVER used
//                            arithmetically here (the amount is already
//                            computed).
//
// Internal-router-only — never added to publicUserTypes. Always
// orchestrator/job-triggered (internal/ledger/worker/accrual.go), never a
// direct end-user or even ops HTTP action.
//
// Capitalization decision (docs/roadmap/archive/19 Task T3 step 5): interest is
// credited straight into the savings account's own balance every day
// (simple daily interest; compounds naturally because tomorrow's snapshot
// already includes today's credit) — there is NO separate "accrued
// interest" holding account or periodic capitalization step in this MVP.
// Moving to accrue-to-a-holding-account + monthly capitalization later is a
// small change (swap the destination account + add a monthly job), not a
// redesign.
//
// [0] = system.interest_expense[currency]
// [1] = savings account
// =============================================================================

type InterestAccrue struct{ repo repository.AccountRepository }

func NewInterestAccrue(r repository.AccountRepository) *InterestAccrue {
	return &InterestAccrue{repo: r}
}
func (p *InterestAccrue) Type() string { return "interest_accrue" }

func (p *InterestAccrue) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	accountID, err := generalutil.MetaUUID(cmd.Metadata, "account_id")
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_accrue: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, accountID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_accrue: currency: %w", err)
	}
	expenseID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeInterestExpense, "", currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("interest_accrue: interest_expense[%s]: %w", currency, err)
	}
	return twoLeg(expenseID, accountID), currency, nil
}

func (p *InterestAccrue) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	// No SufficientFundsValidator: interest_expense (AccountIDs[0], debited
	// here) is allow_negative=true — same pattern as money_in debiting
	// settlement.
	return MultiValidator{PositiveAmountValidator{}, IntegralAmountValidator{}}.Validate(ctx, tx, cmd, bal)
}

func (p *InterestAccrue) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	accrualDate, _ := generalutil.MetaString(cmd.Metadata, "accrual_date")
	rateBps, _ := generalutil.MetaString(cmd.Metadata, "rate_bps")
	note := fmt.Sprintf("interest accrual date=%s rate_bps=%s", accrualDate, rateBps)
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: note},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *InterestAccrue) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *InterestAccrue) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand rejects the command before any DB work if account_id,
// accrual_date, or rate_bps is missing.
func (p *InterestAccrue) ValidateCommand(_ context.Context, cmd Command) error {
	if _, err := generalutil.MetaUUID(cmd.Metadata, "account_id"); err != nil {
		return fmt.Errorf("%w: interest_accrue requires metadata 'account_id'", apperror.ErrValidation)
	}
	if v, err := generalutil.MetaString(cmd.Metadata, "accrual_date"); err != nil || v == "" {
		return fmt.Errorf("%w: interest_accrue requires metadata 'accrual_date'", apperror.ErrValidation)
	}
	if v, err := generalutil.MetaString(cmd.Metadata, "rate_bps"); err != nil || v == "" {
		return fmt.Errorf("%w: interest_accrue requires metadata 'rate_bps'", apperror.ErrValidation)
	}
	return nil
}
