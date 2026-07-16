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
// 13. Chargeback — user.cash → chargeback[card_network]
//
// Chargeback accounts are sharded by card network because Visa, Mastercard,
// and JCB each have separate dispute windows, reason code spaces, and
// settlement reports. One account per network makes reconciliation direct.
//
// Initiated externally by card network / bank. NOT a reversal — it is a
// forced debit reflecting funds the acquiring bank has already pulled back.
// Ops team handles the offsetting reconciliation against the merchant.
//
// NOTE: SufficientFundsValidator intentionally omitted — chargebacks can
//   result in negative user balance (overdraft). Handle via a separate
//   overdrawn_balance event / recovery flow at the orchestration layer.
//
// Metadata: "dispute_ref"   (required) — external dispute ID from card network
//           "card_network"  (required) — "visa" | "mastercard" | "jcb" | "amex"
//           "reason_code"   (optional) — network reason code e.g. "4853", "10.4"
// =============================================================================

type Chargeback struct{ repo repository.AccountRepository }

func NewChargeback(r repository.AccountRepository) *Chargeback { return &Chargeback{repo: r} }
func (p *Chargeback) Type() string                             { return "chargeback" }

func (p *Chargeback) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	disputeRef, err := generalutil.MetaString(cmd.Metadata, "dispute_ref")
	if err != nil || disputeRef == "" {
		return ResolvedAccounts{}, "", fmt.Errorf("%w: chargeback requires metadata 'dispute_ref'", apperror.ErrValidation)
	}
	cardNetwork, err := generalutil.MetaString(cmd.Metadata, "card_network")
	if err != nil || cardNetwork == "" {
		return ResolvedAccounts{}, "", fmt.Errorf("%w: chargeback requires metadata 'card_network' (e.g. 'visa', 'mastercard')", apperror.ErrValidation)
	}

	cashID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("chargeback: user cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, cashID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("chargeback: currency: %w", err)
	}
	// Chargeback account sharded by card network — maps to real dispute settlement rails.
	cbID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeChargeback, cardNetwork, currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("chargeback: system chargeback[%s]: %w", cardNetwork, err)
	}
	return twoLeg(cashID, cbID), currency, nil
}

func (p *Chargeback) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	// dispute_ref and card_network already validated in ResolveAccounts.
	// SufficientFundsValidator intentionally omitted — see design note above.
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *Chargeback) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	disputeRef, _ := generalutil.MetaString(cmd.Metadata, "dispute_ref")
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: "chargeback: " + disputeRef},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *Chargeback) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *Chargeback) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *Chargeback) ValidateCommand(_ context.Context, _ Command) error { return nil }
