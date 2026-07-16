package processors

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
)

// =============================================================================
// 22. Reversal — swap debit↔credit of every entry in an original tx
// =============================================================================

type Reversal struct {
	txRepo  repository.TransactionRepository
	accRepo repository.AccountRepository
}

func NewReversal(txRepo repository.TransactionRepository, accRepo repository.AccountRepository) *Reversal {
	return &Reversal{txRepo: txRepo, accRepo: accRepo}
}
func (p *Reversal) Type() string { return "reversal" }

func (p *Reversal) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	if cmd.ReferenceID == uuid.Nil {
		return ResolvedAccounts{}, "", fmt.Errorf("reversal: ReferenceID (original transaction ID) required")
	}
	accountIDs, err := p.txRepo.GetAccountIDs(ctx, cmd.ReferenceID)
	// NOTE: called before the posting transaction begins — GetAccountIDs is a
	// read-only lookup outside any *sql.Tx (see TransactionRepository doc).
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("reversal: get account IDs from original: %w", err)
	}
	if len(accountIDs) == 0 {
		return ResolvedAccounts{}, "", fmt.Errorf("%w: %s", apperror.ErrOriginalNotFound, cmd.ReferenceID)
	}
	currency, err := p.accRepo.GetAccountCurrency(ctx, accountIDs[0])
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("reversal: currency: %w", err)
	}
	// Source/Destination left uuid.Nil (decision K2, docs/plan/13): a
	// reversal can touch more than two accounts (e.g. reversing a
	// transaction with a fee leg), so there is no single semantic
	// source->destination pair to report — ledger_transactions.source/
	// destination_account_id end up NULL for reversals.
	return ResolvedAccounts{Ordered: accountIDs}, currency, nil
}

func (p *Reversal) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, balances map[uuid.UUID]model.AccountBalance) error {
	origType, status, _, closedBy, err := p.txRepo.GetHeader(ctx, tx, cmd.ReferenceID)
	if err != nil {
		return fmt.Errorf("reversal: get original header: %w", err)
	}
	// [docs/plan/14 Task T2] Reversing a reversal would double-credit the
	// original's counterparty — not a supported correction path (a mistaken
	// reversal is fixed by posting the ORIGINAL transaction's type again,
	// not by reversing the reversal).
	if origType == "reversal" {
		return apperror.NewBizErr(apperror.ErrNotReversible, "cannot reverse a reversal")
	}
	if closedBy != nil {
		return apperror.NewBizErr(apperror.ErrAlreadyReversed, fmt.Sprintf("transaction %s already reversed", cmd.ReferenceID))
	}
	if status != "posted" {
		return apperror.NewBizErr(apperror.ErrNotReversible, fmt.Sprintf("original status is %q, must be 'posted'", status))
	}
	// This is a fast-fail convenience check against an unlocked read — the
	// actual race-proof guard is the CloseOriginal UPDATE the service layer
	// runs after Validate succeeds (docs/plan/14 Task T2, decision K3).
	return nil
}

func (p *Reversal) BuildEntries(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, balances map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	origEntries, err := p.txRepo.GetEntries(ctx, tx, cmd.ReferenceID)
	if err != nil {
		return nil, fmt.Errorf("reversal: fetch original entries: %w", err)
	}
	if len(origEntries) == 0 {
		return nil, fmt.Errorf("%w: no entries for %s", apperror.ErrOriginalNotFound, cmd.ReferenceID)
	}
	for _, e := range origEntries {
		if e.Direction == constant.Credit {
			ab := balances[e.AccountID]
			if ab.Balance.LessThan(e.Amount) {
				return nil, apperror.NewBizErr(apperror.ErrInsufficientFunds,
					fmt.Sprintf("reversal: account %s has %s, needs %s", e.AccountID, ab.Balance, e.Amount))
			}
		}
	}
	// Marking the original 'reversed' is now the service layer's job (step
	// 4b, CloseOriginal) — it runs before BuildEntries and is the atomic
	// guard against double-reversal (docs/plan/14 Task T2). Nothing to do
	// here anymore.
	reversed := make([]model.EntryInstruction, len(origEntries))
	for i, e := range origEntries {
		dir := constant.Credit
		if e.Direction == constant.Credit {
			dir = constant.Debit
		}
		reversed[i] = model.EntryInstruction{
			AccountID: e.AccountID, Direction: dir, Amount: e.Amount,
			Note: fmt.Sprintf("reversal of entry %s", e.EntryID),
		}
	}
	return reversed, nil
}

// OutboxEvents emits two events (docs/plan/14 Task T3): a normal
// ledger.transaction.posted.v1 for the reversal transaction itself (it IS a
// posted transaction, just of type "reversal"), plus a
// ledger.transaction.reversed.v1 routed against the ORIGINAL transaction's
// AggregateID — so a consumer watching one transaction's lifecycle sees the
// reversal without correlating two different aggregate ids itself.
func (p *Reversal) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	posted := newPostedEvent(cmd, txID, entries)
	reversed := events.NewTransactionReversed(txID, cmd.ReferenceID, cmd.Amount.String(), cmd.Currency, time.Now().UTC())
	return []model.OutboxEvent{
		posted,
		{
			AggregateType: "ledger_transaction", AggregateID: cmd.ReferenceID,
			EventType: events.TypeTransactionReversed, Payload: reversed.ToPayload(),
		},
	}
}
func (p *Reversal) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *Reversal) ValidateCommand(_ context.Context, _ Command) error { return nil }
