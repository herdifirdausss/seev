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
// 24. AdjustmentSuspenseDebit — system.suspense[gateway] → system.adjustment
//
// AdjustmentSuspenseCredit's mirror (docs/roadmap/archive/16 Task T2, decision K5): used
// when a gateway's settlement report shows LESS than the ledger recorded
// (match_status 'amount_mismatch' the other direction, or 'missing_external'
// where an internal transaction has no counterpart in the report).
//
// Metadata: same as AdjustmentSuspenseCredit, including the optional
// "currency" key (defaults to "IDR" — see that processor's doc comment).
// =============================================================================

type AdjustmentSuspenseDebit struct{ repo repository.AccountRepository }

func NewAdjustmentSuspenseDebit(r repository.AccountRepository) *AdjustmentSuspenseDebit {
	return &AdjustmentSuspenseDebit{repo: r}
}
func (p *AdjustmentSuspenseDebit) Type() string { return "adjustment_suspense_debit" }

func (p *AdjustmentSuspenseDebit) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	gateway, err := requireGateway(cmd, "adjustment_suspense_debit")
	if err != nil {
		return ResolvedAccounts{}, "", err
	}
	currency, err := generalutil.MetaString(cmd.Metadata, "currency")
	if err != nil || currency == "" {
		currency = "IDR" // see AdjustmentSuspenseCredit's doc comment — no user account to derive currency from
	}
	suspenseID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeSuspense, suspenseQualifier(gateway), currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("adjustment_suspense_debit: suspense account: %w", err)
	}
	adjID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeAdjustment, "", currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("adjustment_suspense_debit: adjustment account: %w", err)
	}
	return twoLeg(suspenseID, adjID), currency, nil
}

func (p *AdjustmentSuspenseDebit) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if _, err := generalutil.MetaString(cmd.Metadata, "authorized_by"); err != nil {
		return fmt.Errorf("%w: adjustment_suspense_debit requires metadata 'authorized_by'", apperror.ErrValidation)
	}
	return MultiValidator{PositiveAmountValidator{}, IntegralAmountValidator{}}.Validate(ctx, tx, cmd, bal)
}

func (p *AdjustmentSuspenseDebit) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	reason, _ := generalutil.MetaString(cmd.Metadata, "reason")
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: fmt.Sprintf("suspense adj debit reason=%s", reason)},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *AdjustmentSuspenseDebit) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *AdjustmentSuspenseDebit) AfterCommit(_ context.Context, _ Command) error { return nil }

func (p *AdjustmentSuspenseDebit) ValidateCommand(_ context.Context, _ Command) error { return nil }
