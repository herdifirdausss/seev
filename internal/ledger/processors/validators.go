package processors

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/shopspring/decimal"
)

// Validator is a composable business rule unit. [FIX #14 moved here from registry.go]
type Validator interface {
	Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, balances map[uuid.UUID]model.AccountBalance) error
}

// =============================================================================
// MultiValidator — composable validator chain
// =============================================================================

// MultiValidator runs validators sequentially, stopping at the first error.
type MultiValidator []Validator

func (m MultiValidator) Validate(
	ctx context.Context, tx *sql.Tx,
	cmd ResolvedCommand, balances map[uuid.UUID]model.AccountBalance,
) error {
	for _, v := range m {
		if err := v.Validate(ctx, tx, cmd, balances); err != nil {
			return err
		}
	}
	return nil
}

// =============================================================================
// Built-in Validators
// =============================================================================

// PositiveAmountValidator ensures Amount > 0.
type PositiveAmountValidator struct{}

func (PositiveAmountValidator) Validate(_ context.Context, _ *sql.Tx,
	cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) error {
	if !cmd.Amount.IsPositive() {
		return apperror.NewBizErr(apperror.ErrAmountTooSmall, fmt.Sprintf("amount must be > 0, got %s", cmd.Amount))
	}
	return nil
}

// IntegralAmountValidator ensures Amount has no fractional part — the
// ledger is minor-unit-only (docs/plan/01 decision D2). This is a
// second line of defense behind transport's decimalFromString rejecting
// fractional input (docs/plan/10 Task T4): every processor validates it
// too, so a caller that reaches Handle() directly (a future in-process
// module, not just HTTP) can't slip a fractional amount past the
// repository layer, where account_balance_repository.UpdateBalances would
// otherwise have to choose between silently truncating (losing/creating
// money) or failing deep inside a transaction that already touched the DB.
type IntegralAmountValidator struct{}

func (IntegralAmountValidator) Validate(_ context.Context, _ *sql.Tx,
	cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) error {
	if !cmd.Amount.Equal(cmd.Amount.Truncate(0)) {
		return apperror.NewBizErr(apperror.ErrValidation, fmt.Sprintf("amount must be an integer (minor units), got %s", cmd.Amount))
	}
	return nil
}

// MinAmountValidator ensures Amount >= Min.
type MinAmountValidator struct{ Min decimal.Decimal }

func (v MinAmountValidator) Validate(_ context.Context, _ *sql.Tx,
	cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) error {
	if cmd.Amount.LessThan(v.Min) {
		return apperror.NewBizErr(apperror.ErrAmountTooSmall, fmt.Sprintf("minimum is %s, got %s", v.Min, cmd.Amount))
	}
	return nil
}

// MaxAmountValidator ensures Amount <= Max.
type MaxAmountValidator struct{ Max decimal.Decimal }

func (v MaxAmountValidator) Validate(_ context.Context, _ *sql.Tx,
	cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) error {
	if cmd.Amount.GreaterThan(v.Max) {
		return apperror.NewBizErr(apperror.ErrAmountTooLarge, fmt.Sprintf("maximum is %s, got %s", v.Max, cmd.Amount))
	}
	return nil
}

// SufficientFundsValidator checks that a specific account has enough balance.
type SufficientFundsValidator struct{ AccountID uuid.UUID }

func (v SufficientFundsValidator) Validate(_ context.Context, _ *sql.Tx,
	cmd ResolvedCommand, balances map[uuid.UUID]model.AccountBalance) error {
	ab, ok := balances[v.AccountID]
	if !ok {
		return fmt.Errorf("%w: %s", apperror.ErrAccountNotFound, v.AccountID)
	}
	if ab.Balance.LessThan(cmd.Amount) {
		return apperror.NewBizErr(apperror.ErrInsufficientFunds,
			fmt.Sprintf("account %s has %s, needs %s", v.AccountID, ab.Balance, cmd.Amount))
	}
	return nil
}

// NotSelfTransferValidator ensures two account IDs are different.
type NotSelfTransferValidator struct{ A, B uuid.UUID }

func (v NotSelfTransferValidator) Validate(_ context.Context, _ *sql.Tx,
	_ ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) error {
	if v.A == v.B && v.A != uuid.Nil {
		return apperror.NewBizErr(apperror.ErrSelfTransfer, fmt.Sprintf("both accounts are %s", v.A))
	}
	return nil
}

// FeeAmountValidator checks that fee_amount metadata is valid and < total amount.
type FeeAmountValidator struct{}

func (FeeAmountValidator) Validate(_ context.Context, _ *sql.Tx,
	cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) error {
	fee, err := generalutil.MetaDecimal(cmd.Metadata, "fee_amount")
	if err != nil {
		return fmt.Errorf("%w: %s", apperror.ErrValidation, err)
	}
	if !fee.IsPositive() {
		return apperror.NewBizErr(apperror.ErrValidation, fmt.Sprintf("fee_amount must be positive, got %s", fee))
	}
	if fee.GreaterThanOrEqual(cmd.Amount) {
		return apperror.NewBizErr(apperror.ErrFeeExceedsAmount, fmt.Sprintf("fee %s >= total %s", fee, cmd.Amount))
	}
	return nil
}
