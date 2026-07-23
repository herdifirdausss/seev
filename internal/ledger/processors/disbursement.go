package processors

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// =============================================================================
// Disbursement — system.settlement[platform][currency] -> user.cash
// (docs/roadmap/archive/19 Task T2 step 3)
//
// A platform-initiated payout to a user (payroll, mass refund) — distinct
// from money_in (which represents a real external deposit reconciled
// against a gateway settlement report). Sourced from settlement[platform]
// by default: the platform's own funds moving out, sharded by currency
// exactly like every other settlement account (migrations/000015).
//
// Metadata: "batch_id", "item_no" (both optional, informational only —
// recorded in the entry note for audit; the CALLER's idempotency key,
// "batch:<batch_id>:<item_no>", is what actually prevents double-posting,
// not this metadata).
//
// Internal-router-only — never added to publicUserTypes. A batch
// disbursement is always operator/ops-triggered via the admin endpoints in
// internal/ledger/service/disbursement, never a direct end-user action.
//
// [0] = settlement[platform][currency]
// [1] = user.cash
// =============================================================================

type Disbursement struct{ repo repository.AccountRepository }

func NewDisbursement(r repository.AccountRepository) *Disbursement { return &Disbursement{repo: r} }
func (p *Disbursement) Type() string                               { return "disbursement" }

func (p *Disbursement) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	cashID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("disbursement: user cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, cashID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("disbursement: currency: %w", err)
	}
	settlementID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeSettlement, "platform", currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("disbursement: settlement[platform][%s]: %w", currency, err)
	}
	return twoLeg(settlementID, cashID), currency, nil
}

func (p *Disbursement) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	return MultiValidator{PositiveAmountValidator{}, IntegralAmountValidator{}}.Validate(ctx, tx, cmd, bal)
}

func (p *Disbursement) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	batchID, _ := generalutil.MetaString(cmd.Metadata, "batch_id")
	itemNo, _ := generalutil.MetaString(cmd.Metadata, "item_no")
	note := fmt.Sprintf("disbursement batch=%s item=%s", batchID, itemNo)
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: note},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *Disbursement) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *Disbursement) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *Disbursement) ValidateCommand(_ context.Context, _ Command) error { return nil }
