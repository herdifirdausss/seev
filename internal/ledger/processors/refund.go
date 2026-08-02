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
// 11. Refund — merchant.settlement → user.cash
// Metadata: "merchant_account_id" (UUID string)
//
// ReferenceID (the original charge's transaction ID) is required —
// business-completeness audit finding: unlike Reversal/EscrowRefund/
// WithdrawSettle/WithdrawCancel/EscrowRelease (all wired into
// lifecycleCloseReason, internal/ledger/service/handle/service.go), Refund
// used to post a standalone transaction with no DB-level link back to the
// charge it refunds. closed_reason's CHECK constraint
// (migrations/ledger/000004_lifecycle_guard.up.sql) already allowed
// 'refunded' (EscrowRefund already uses it), so this closes an existing gap
// rather than adding a new mechanism. No production caller invokes type
// "refund" yet, so requiring ReferenceID here breaks nothing existing.
// =============================================================================

type Refund struct {
	repo   repository.AccountRepository
	txRepo repository.TransactionRepository
}

func NewRefund(r repository.AccountRepository, txRepo repository.TransactionRepository) *Refund {
	return &Refund{repo: r, txRepo: txRepo}
}
func (p *Refund) Type() string { return "refund" }

func (p *Refund) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	if cmd.TargetUserID == uuid.Nil {
		return ResolvedAccounts{}, "", fmt.Errorf("refund: TargetUserID (refund receiver) required")
	}
	merchantID, err := generalutil.MetaUUID(cmd.Metadata, "merchant_account_id")
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("refund: %w", err)
	}
	userCashID, err := p.repo.GetAccountID(ctx, cmd.TargetUserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("refund: user cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, userCashID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("refund: currency: %w", err)
	}
	return twoLeg(merchantID, userCashID), currency, nil
}

func (p *Refund) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	// Same fast-fail convenience check as Reversal.Validate (reversal.go) —
	// the race-proof guard is the service layer's CloseOriginal UPDATE
	// (docs/roadmap/archive/14 Task T2, decision K3), which runs after this
	// and is what actually prevents two concurrent refunds of the same charge.
	_, status, _, closedBy, err := p.txRepo.GetHeader(ctx, tx, cmd.ReferenceID)
	if err != nil {
		return fmt.Errorf("refund: get original header: %w", err)
	}
	if closedBy != nil {
		return apperror.NewBizErr(apperror.ErrAlreadyClosed, fmt.Sprintf("transaction %s already closed", cmd.ReferenceID))
	}
	if status != "posted" {
		return apperror.NewBizErr(apperror.ErrNotReversible, fmt.Sprintf("original status is %q, must be 'posted'", status))
	}
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *Refund) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: fmt.Sprintf("refund of %s to user", cmd.ReferenceID)},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *Refund) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *Refund) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand requires ReferenceID (the original charge being refunded),
// the same shared pre-DB check as EscrowRefund/WithdrawSettle/etc.
func (p *Refund) ValidateCommand(_ context.Context, cmd Command) error {
	return requireReferenceID(cmd, "refund")
}
