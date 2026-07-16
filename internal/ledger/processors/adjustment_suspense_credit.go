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
// 23. AdjustmentSuspenseCredit — system.adjustment → system.suspense[gateway]
//
// Reconciliation resolution (docs/plan/16 Task T2, decision K5): posted ONLY
// through the maker-checker flow (internal/ledger/service/adjustments), same
// as AdjustmentCredit/Debit — never reachable via direct POST. Used when a
// gateway's settlement report shows money the ledger has no record of
// (match_status 'missing_internal'/'amount_mismatch' where the report is
// higher): credits the per-gateway suspense account rather than a user's
// cash account, since a recon discrepancy is a settlement-level bookkeeping
// gap, not a specific user's balance.
//
// Metadata: "gateway"       - required, which suspense[gateway] account
//           "authorized_by" - employee ID (the approver, injected by
//                              adjustments.Service.Approve)
//           "reason"        - free text, expected to reference recon_items.id
//           "currency"      - optional, defaults to "IDR" (docs/plan/18 Task
//                              T2). This processor has no user-facing account
//                              to derive currency from (both legs are system
//                              accounts), so it is the one place in the
//                              codebase where an IDR default is still allowed
//                              — every recon batch is currently IDR-only
//                              (docs/plan/16 Task T2); a multi-currency recon
//                              caller must pass "currency" explicitly.
// =============================================================================

type AdjustmentSuspenseCredit struct{ repo repository.AccountRepository }

func NewAdjustmentSuspenseCredit(r repository.AccountRepository) *AdjustmentSuspenseCredit {
	return &AdjustmentSuspenseCredit{repo: r}
}
func (p *AdjustmentSuspenseCredit) Type() string { return "adjustment_suspense_credit" }

func (p *AdjustmentSuspenseCredit) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	gateway, err := requireGateway(cmd, "adjustment_suspense_credit")
	if err != nil {
		return ResolvedAccounts{}, "", err
	}
	currency, err := generalutil.MetaString(cmd.Metadata, "currency")
	if err != nil || currency == "" {
		currency = "IDR" // see doc comment above — no user account to derive currency from
	}
	adjID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeAdjustment, "", currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("adjustment_suspense_credit: adjustment account: %w", err)
	}
	suspenseID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeSuspense, suspenseQualifier(gateway), currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("adjustment_suspense_credit: suspense account: %w", err)
	}
	return twoLeg(adjID, suspenseID), currency, nil
}

func (p *AdjustmentSuspenseCredit) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if _, err := generalutil.MetaString(cmd.Metadata, "authorized_by"); err != nil {
		return fmt.Errorf("%w: adjustment_suspense_credit requires metadata 'authorized_by'", apperror.ErrValidation)
	}
	return MultiValidator{PositiveAmountValidator{}, IntegralAmountValidator{}}.Validate(ctx, tx, cmd, bal)
}

func (p *AdjustmentSuspenseCredit) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	reason, _ := generalutil.MetaString(cmd.Metadata, "reason")
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: fmt.Sprintf("suspense adj credit reason=%s", reason)},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *AdjustmentSuspenseCredit) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *AdjustmentSuspenseCredit) AfterCommit(_ context.Context, _ Command) error { return nil }

func (p *AdjustmentSuspenseCredit) ValidateCommand(_ context.Context, _ Command) error { return nil }
