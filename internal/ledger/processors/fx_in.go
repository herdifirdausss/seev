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
// FxIn — fx_conversion[pair][ccy2] -> user.cash[ccy2] (docs/roadmap/archive/18 Task T3)
//
// FxOut's mirror — completes the conversion by crediting the user's
// TARGET-currency cash from the platform's FX position account. A SEPARATE
// ledger transaction from FxOut, with its own idempotency key
// ("fx:<quote_id>:in") — see FxOut's doc comment for the full invariant
// (one transaction = one currency; cross-currency "balance" is an FX
// position tracked outside the ledger, not enforced by it).
//
// Debiting fx_conversion here never fails on insufficient funds — both
// currency members are allow_negative=true (migrations/000013), since the
// platform's FX position is expected to run either direction depending on
// order flow. If THIS leg fails for another reason (e.g. the destination
// cash account is suspended) after FxOut already posted, the pair's
// position becomes visibly open (a non-zero fx_conversion balance) — see
// docs/operations/runbooks/fx-position.md for the human decision procedure (retry this
// leg, or reverse FxOut).
//
// Metadata: identical contract to FxOut (quote_id, rate, pair — all
// required, "rate" never used arithmetically here either).
//
// Reachable only via the internal router — never added to publicUserTypes.
//
// [0] = fx_conversion[pair][ccy2]
// [1] = user.cash[ccy2]
// =============================================================================

type FxIn struct{ repo repository.AccountRepository }

func NewFxIn(r repository.AccountRepository) *FxIn { return &FxIn{repo: r} }
func (p *FxIn) Type() string                       { return "fx_in" }

func (p *FxIn) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	pair, err := generalutil.MetaString(cmd.Metadata, "pair")
	if err != nil || pair == "" {
		return ResolvedAccounts{}, "", fmt.Errorf("%w: fx_in requires metadata 'pair' (e.g. 'IDRUSD')", apperror.ErrValidation)
	}
	cashID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("fx_in: user cash: %w", err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, cashID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("fx_in: currency: %w", err)
	}
	fxID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeFxConversion, pair, currency)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("fx_in: fx_conversion[%s][%s]: %w", pair, currency, err)
	}
	return twoLeg(fxID, cashID), currency, nil
}

func (p *FxIn) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	// No SufficientFundsValidator: fx_conversion (AccountIDs[0], debited
	// here) is allow_negative=true on both currency members — same pattern
	// as money_in debiting settlement.
	return MultiValidator{PositiveAmountValidator{}, IntegralAmountValidator{}}.Validate(ctx, tx, cmd, bal)
}

func (p *FxIn) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	quoteID, _ := generalutil.MetaString(cmd.Metadata, "quote_id")
	rate, _ := generalutil.MetaString(cmd.Metadata, "rate")
	pair, _ := generalutil.MetaString(cmd.Metadata, "pair")
	note := fmt.Sprintf("fx_in pair=%s quote=%s rate=%s", pair, quoteID, rate)
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: note},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *FxIn) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *FxIn) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand — identical contract to FxOut's.
func (p *FxIn) ValidateCommand(_ context.Context, cmd Command) error {
	if v, err := generalutil.MetaString(cmd.Metadata, "quote_id"); err != nil || v == "" {
		return fmt.Errorf("%w: fx_in requires metadata 'quote_id'", apperror.ErrValidation)
	}
	if v, err := generalutil.MetaString(cmd.Metadata, "rate"); err != nil || v == "" {
		return fmt.Errorf("%w: fx_in requires metadata 'rate'", apperror.ErrValidation)
	}
	if v, err := generalutil.MetaString(cmd.Metadata, "pair"); err != nil || v == "" {
		return fmt.Errorf("%w: fx_in requires metadata 'pair'", apperror.ErrValidation)
	}
	return nil
}
