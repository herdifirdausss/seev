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
// FxOut — user.cash[ccy1] -> fx_conversion[pair][ccy1] (docs/roadmap/archive/18 Task T3)
//
// FX is NOT a ledger feature — a conversion is an orchestrator moving money
// through two ordinary ledger transactions: FxOut takes the source-currency
// leg out of the user's cash into the platform's FX position account, FxIn
// (a SEPARATE transaction, its own idempotency key) puts the target-currency
// leg into the user's cash from the position account on the other side.
//
// INVARIANT: one ledger transaction is always one currency. FxOut's entries
// are entirely in ccy1; FxIn's are entirely in ccy2. fn_verify_ledger_balance
// stays meaningful per-transaction. Nothing in the ledger enforces that a
// FxOut/FxIn pair nets to zero across currencies — that "balance" is an FX
// position, tracked by finance/ops via the fx_conversion account balances,
// not by this package. See docs/operations/runbooks/fx-position.md for the manual
// procedure when a pair's legs diverge (one side posts, the other fails).
//
// Metadata (all required — ValidateCommand rejects without them):
//   "quote_id" — orchestrator's quote/conversion id. Used by the caller to
//                derive this transaction's idempotency key ("fx:<quote_id>:out")
//                — NOT read or validated by this processor itself.
//   "rate"     — decimal string, stored verbatim as an audit trail. NEVER
//                used arithmetically here: both legs' amounts are computed
//                by the orchestrator before either Command is built.
//   "pair"     — e.g. "IDRUSD" — selects the fx_conversion account family
//                (shared qualifier across both currency members, see
//                migrations/000013_fx_accounts.up.sql).
//
// Reachable only via the internal router (trusted orchestrator caller) —
// never added to publicUserTypes.
//
// [0] = user.cash[ccy1]
// [1] = fx_conversion[pair][ccy1]
// =============================================================================

type FxOut struct{ repo repository.AccountRepository }

func NewFxOut(r repository.AccountRepository) *FxOut { return &FxOut{repo: r} }
func (p *FxOut) Type() string                        { return "fx_out" }

func (p *FxOut) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	pair, err := generalutil.MetaString(cmd.Metadata, "pair")
	if err != nil || pair == "" {
		return ResolvedAccounts{}, "", fmt.Errorf("%w: fx_out requires metadata 'pair' (e.g. 'IDRUSD')", apperror.ErrValidation)
	}
	cashID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("fx_out: user cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, cashID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("fx_out: currency: %w", err)
	}
	fxID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeFxConversion, pair, currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("fx_out: fx_conversion[%s][%s]: %w", pair, currency, err)
	}
	return twoLeg(cashID, fxID), currency, nil
}

func (p *FxOut) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *FxOut) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	quoteID, _ := generalutil.MetaString(cmd.Metadata, "quote_id")
	rate, _ := generalutil.MetaString(cmd.Metadata, "rate")
	pair, _ := generalutil.MetaString(cmd.Metadata, "pair")
	note := fmt.Sprintf("fx_out pair=%s quote=%s rate=%s", pair, quoteID, rate)
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: note},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *FxOut) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *FxOut) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand rejects the command before any DB work if quote_id, rate,
// or pair is missing — the orchestrator's audit trail (rate) and its own
// idempotency-key derivation (quote_id) both depend on these being present.
func (p *FxOut) ValidateCommand(_ context.Context, cmd Command) error {
	if v, err := generalutil.MetaString(cmd.Metadata, "quote_id"); err != nil || v == "" {
		return fmt.Errorf("%w: fx_out requires metadata 'quote_id'", apperror.ErrValidation)
	}
	if v, err := generalutil.MetaString(cmd.Metadata, "rate"); err != nil || v == "" {
		return fmt.Errorf("%w: fx_out requires metadata 'rate'", apperror.ErrValidation)
	}
	if v, err := generalutil.MetaString(cmd.Metadata, "pair"); err != nil || v == "" {
		return fmt.Errorf("%w: fx_out requires metadata 'pair'", apperror.ErrValidation)
	}
	return nil
}
