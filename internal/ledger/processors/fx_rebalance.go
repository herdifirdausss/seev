package processors

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/shopspring/decimal"
)

// FXRebalanceCredit is the governed local synthetic position increase:
// system.adjustment[currency] -> system.fx_conversion[pair][currency]. It is
// only reachable after the maker-checker adjustment workflow approves the
// request; callers never update account_balances directly.
type FXRebalanceCredit struct{ repo repository.AccountRepository }

func NewFXRebalanceCredit(r repository.AccountRepository) *FXRebalanceCredit {
	return &FXRebalanceCredit{repo: r}
}

func (p *FXRebalanceCredit) Type() string { return "fx_rebalance_credit" }

func (p *FXRebalanceCredit) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	pair, code, err := fxRebalanceTarget(cmd)
	if err != nil {
		return ResolvedAccounts{}, "", err
	}
	adjustmentID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeAdjustment, "", code)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("fx_rebalance_credit: adjustment account: %w", err)
	}
	positionID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeFxConversion, pair, code)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("fx_rebalance_credit: FX position account: %w", err)
	}
	return twoLeg(adjustmentID, positionID), code, nil
}

func (p *FXRebalanceCredit) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, balances map[uuid.UUID]model.AccountBalance) error {
	if len(cmd.AccountIDs) < 2 {
		return fmt.Errorf("%w: fx_rebalance_credit requires source and position accounts", apperror.ErrValidation)
	}
	if _, err := generalutil.MetaString(cmd.Metadata, "authorized_by"); err != nil {
		return fmt.Errorf("%w: fx_rebalance_credit requires metadata 'authorized_by'", apperror.ErrValidation)
	}
	if _, err := generalutil.MetaString(cmd.Metadata, "reason"); err != nil {
		return fmt.Errorf("%w: fx_rebalance_credit requires metadata 'reason'", apperror.ErrValidation)
	}
	if err := MultiValidator{PositiveAmountValidator{}, IntegralAmountValidator{}}.Validate(ctx, tx, cmd, nil); err != nil {
		return err
	}
	return validateFXRebalancePosition(ctx, tx, cmd, balances, cmd.AccountIDs[1], true)
}

func (p *FXRebalanceCredit) ValidateBeforeBalanceLocks(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand) error {
	return lockFXRebalancePositionLimit(ctx, tx, cmd)
}

func (p *FXRebalanceCredit) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	pair, _ := generalutil.MetaString(cmd.Metadata, "pair")
	reason, _ := generalutil.MetaString(cmd.Metadata, "reason")
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: fmt.Sprintf("FX rebalance credit pair=%s reason=%s", pair, reason)},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *FXRebalanceCredit) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}

func (p *FXRebalanceCredit) AfterCommit(_ context.Context, _ Command) error { return nil }

func (p *FXRebalanceCredit) ValidateCommand(_ context.Context, cmd Command) error {
	_, _, err := fxRebalanceTarget(cmd)
	return err
}

// FXRebalanceDebit is the governed local synthetic position decrease:
// system.fx_conversion[pair][currency] -> system.adjustment[currency].
type FXRebalanceDebit struct{ repo repository.AccountRepository }

func NewFXRebalanceDebit(r repository.AccountRepository) *FXRebalanceDebit {
	return &FXRebalanceDebit{repo: r}
}

func (p *FXRebalanceDebit) Type() string { return "fx_rebalance_debit" }

func (p *FXRebalanceDebit) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	pair, code, err := fxRebalanceTarget(cmd)
	if err != nil {
		return ResolvedAccounts{}, "", err
	}
	positionID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeFxConversion, pair, code)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("fx_rebalance_debit: FX position account: %w", err)
	}
	adjustmentID, err := p.repo.GetSystemAccountID(ctx, constant.AccountTypeAdjustment, "", code)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("fx_rebalance_debit: adjustment account: %w", err)
	}
	return twoLeg(positionID, adjustmentID), code, nil
}

func (p *FXRebalanceDebit) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, balances map[uuid.UUID]model.AccountBalance) error {
	if len(cmd.AccountIDs) < 2 {
		return fmt.Errorf("%w: fx_rebalance_debit requires position and destination accounts", apperror.ErrValidation)
	}
	if _, err := generalutil.MetaString(cmd.Metadata, "authorized_by"); err != nil {
		return fmt.Errorf("%w: fx_rebalance_debit requires metadata 'authorized_by'", apperror.ErrValidation)
	}
	if _, err := generalutil.MetaString(cmd.Metadata, "reason"); err != nil {
		return fmt.Errorf("%w: fx_rebalance_debit requires metadata 'reason'", apperror.ErrValidation)
	}
	if err := MultiValidator{PositiveAmountValidator{}, IntegralAmountValidator{}}.Validate(ctx, tx, cmd, nil); err != nil {
		return err
	}
	return validateFXRebalancePosition(ctx, tx, cmd, balances, cmd.AccountIDs[0], false)
}

func (p *FXRebalanceDebit) ValidateBeforeBalanceLocks(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand) error {
	return lockFXRebalancePositionLimit(ctx, tx, cmd)
}

func (p *FXRebalanceDebit) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	pair, _ := generalutil.MetaString(cmd.Metadata, "pair")
	reason, _ := generalutil.MetaString(cmd.Metadata, "reason")
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount, Note: fmt.Sprintf("FX rebalance debit pair=%s reason=%s", pair, reason)},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *FXRebalanceDebit) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}

func (p *FXRebalanceDebit) AfterCommit(_ context.Context, _ Command) error { return nil }

func (p *FXRebalanceDebit) ValidateCommand(_ context.Context, cmd Command) error {
	_, _, err := fxRebalanceTarget(cmd)
	return err
}

func fxRebalanceTarget(cmd Command) (pair, code string, err error) {
	pair, pairErr := generalutil.MetaString(cmd.Metadata, "pair")
	if pairErr != nil || strings.TrimSpace(pair) == "" {
		return "", "", fmt.Errorf("%w: FX rebalance requires metadata 'pair'", apperror.ErrValidation)
	}
	pair = strings.ToUpper(strings.TrimSpace(pair))
	code, codeErr := generalutil.MetaString(cmd.Metadata, "currency")
	if codeErr != nil || strings.TrimSpace(code) == "" {
		return "", "", fmt.Errorf("%w: FX rebalance requires metadata 'currency'", apperror.ErrValidation)
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if err := currency.ValidateCode(code); err != nil {
		return "", "", fmt.Errorf("%w: %v", apperror.ErrCurrencyInvalid, err)
	}
	if cmd.Currency != "" && strings.ToUpper(strings.TrimSpace(cmd.Currency)) != code {
		return "", "", fmt.Errorf("%w: command currency does not match rebalance currency", apperror.ErrCurrencyMismatch)
	}
	return pair, code, nil
}

func lockFXRebalancePositionLimit(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand) error {
	pair, code, err := fxRebalanceTarget(cmd.Command)
	if err != nil {
		return err
	}
	positionIndex := 1
	if cmd.Type == "fx_rebalance_debit" {
		positionIndex = 0
	}
	if cmd.Type != "fx_rebalance_credit" && cmd.Type != "fx_rebalance_debit" {
		return fmt.Errorf("%w: unsupported FX rebalance type %q", apperror.ErrValidation, cmd.Type)
	}
	if len(cmd.AccountIDs) <= positionIndex {
		return fmt.Errorf("%w: FX rebalance position account is missing", apperror.ErrValidation)
	}
	var minimum, maximum int64
	err = tx.QueryRowContext(ctx, `
		SELECT l.minimum_balance, l.maximum_balance
		FROM fx_position_limits l
		JOIN fx_pairs p ON p.id = l.pair_id
		WHERE p.position_qualifier = $1
		  AND p.status <> 'disabled'
		  AND l.currency = $2
		FOR UPDATE OF l`, pair, code).Scan(&minimum, &maximum)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: no FX position limit for %s/%s", apperror.ErrFXPositionLimitExceeded, pair, code)
		}
		return fmt.Errorf("lock FX position limit: %w", err)
	}
	if minimum >= maximum {
		return fmt.Errorf("%w: invalid FX position limit for %s/%s", apperror.ErrFXPositionLimitExceeded, pair, code)
	}

	// FX position accounts are system accounts and are consequently not part
	// of the generic user-balance lock set. Lock the exact position row here so
	// the hard-limit check and the eventual system delta are serialized with
	// concurrent rebalances. The limit row is intentionally locked first; FX
	// conversion uses the same pair-limit -> position-balance order.
	var positionID uuid.UUID
	err = tx.QueryRowContext(ctx, `
		SELECT account_id
		FROM account_balances
		WHERE account_id = $1
		FOR UPDATE`, cmd.AccountIDs[positionIndex]).Scan(&positionID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: FX position account %s balance is missing", apperror.ErrAccountNotFound, cmd.AccountIDs[positionIndex])
		}
		return fmt.Errorf("lock FX position balance: %w", err)
	}
	if positionID != cmd.AccountIDs[positionIndex] {
		return fmt.Errorf("%w: FX position account mismatch", apperror.ErrAccountNotFound)
	}
	return nil
}

func validateFXRebalancePosition(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, balances map[uuid.UUID]model.AccountBalance, positionID uuid.UUID, increase bool) error {
	pair, code, err := fxRebalanceTarget(cmd.Command)
	if err != nil {
		return err
	}
	var minimum, maximum int64
	err = tx.QueryRowContext(ctx, `
		SELECT l.minimum_balance, l.maximum_balance
		FROM fx_position_limits l
		JOIN fx_pairs p ON p.id = l.pair_id
		WHERE p.position_qualifier = $1
		  AND p.status <> 'disabled'
		  AND l.currency = $2`, pair, code).Scan(&minimum, &maximum)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: no FX position limit for %s/%s", apperror.ErrFXPositionLimitExceeded, pair, code)
		}
		return fmt.Errorf("load FX position limit: %w", err)
	}

	balance, ok := balances[positionID]
	if !ok {
		return fmt.Errorf("%w: FX position account %s", apperror.ErrAccountNotFound, positionID)
	}
	if !balance.Balance.IsInteger() {
		return fmt.Errorf("%w: FX position balance is not an integer: %s", apperror.ErrMoneyOverflow, balance.Balance)
	}
	projected := balance.Balance
	if increase {
		projected = projected.Add(cmd.Amount)
	} else {
		projected = projected.Sub(cmd.Amount)
	}
	minimumDecimal := decimal.NewFromInt(minimum)
	maximumDecimal := decimal.NewFromInt(maximum)
	if projected.LessThan(minimumDecimal) || projected.GreaterThan(maximumDecimal) {
		return fmt.Errorf("%w: %s projected balance outside [%d,%d]", apperror.ErrFXPositionLimitExceeded, code, minimum, maximum)
	}
	return nil
}
