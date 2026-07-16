package processors

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
)

// =============================================================================
// 16. EscrowRefund — escrow[currency] → buyer.cash (order cancelled)
//
// ReferenceID (required) must point at the escrow_hold this refunds
// (docs/plan/14 Task T2) — full amount only, no partial refund in MVP.
// =============================================================================

type EscrowRefund struct {
	repo   repository.AccountRepository
	txRepo repository.TransactionRepository
}

func NewEscrowRefund(r repository.AccountRepository, txRepo repository.TransactionRepository) *EscrowRefund {
	return &EscrowRefund{repo: r, txRepo: txRepo}
}
func (p *EscrowRefund) Type() string { return "escrow_refund" }

func (p *EscrowRefund) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	if cmd.TargetUserID == uuid.Nil {
		return ResolvedAccounts{}, "", fmt.Errorf("escrow_refund: TargetUserID (buyer) required")
	}
	buyerCashID, err := p.repo.GetAccountID(ctx, cmd.TargetUserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("escrow_refund: buyer cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, buyerCashID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("escrow_refund: currency: %w", err)
	}
	// Select the correct escrow shard by currency — matches the original escrow_hold.
	escrowID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeEscrow, currency, currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("escrow_refund: escrow[%s]: %w", currency, err)
	}
	return twoLeg(escrowID, buyerCashID), currency, nil
}

func (p *EscrowRefund) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	if err := validateOriginalForClose(ctx, tx, p.txRepo, cmd.ReferenceID, "escrow_hold", cmd.Amount); err != nil {
		return err
	}
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *EscrowRefund) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: "escrow refund to buyer"},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *EscrowRefund) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *EscrowRefund) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand requires ReferenceID (docs/plan/14 Task T2) — the
// escrow_hold this refund is closing.
func (p *EscrowRefund) ValidateCommand(_ context.Context, cmd Command) error {
	return requireReferenceID(cmd, "escrow_refund")
}
