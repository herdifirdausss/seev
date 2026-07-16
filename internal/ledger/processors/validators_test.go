package processors

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestMultiValidator_AllPass(t *testing.T) {

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(10),
		},
	}

	mv := MultiValidator{
		PositiveAmountValidator{},
		MinAmountValidator{Min: decimal.NewFromInt(1)},
	}

	err := mv.Validate(context.Background(), nil, cmd, nil)

	assert.NoError(t, err)
}

func TestMultiValidator_StopOnError(t *testing.T) {

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.Zero,
		},
	}

	mv := MultiValidator{
		PositiveAmountValidator{},
		MinAmountValidator{Min: decimal.NewFromInt(1)},
	}

	err := mv.Validate(context.Background(), nil, cmd, nil)

	assert.Error(t, err)
}

func TestPositiveAmountValidator(t *testing.T) {

	v := PositiveAmountValidator{}

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(10),
		},
	}

	err := v.Validate(context.Background(), nil, cmd, nil)

	assert.NoError(t, err)
}

func TestPositiveAmountValidator_Error(t *testing.T) {

	v := PositiveAmountValidator{}

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.Zero,
		},
	}

	err := v.Validate(context.Background(), nil, cmd, nil)

	assert.Error(t, err)
}

// ─── IntegralAmountValidator (docs/plan/10 Task T4) ────────────────────────────

func TestIntegralAmountValidator_IntegerAmount_OK(t *testing.T) {
	v := IntegralAmountValidator{}
	cmd := ResolvedCommand{Command: Command{Amount: decimal.NewFromInt(100)}}

	err := v.Validate(context.Background(), nil, cmd, nil)

	assert.NoError(t, err)
}

func TestIntegralAmountValidator_FractionalAmount_Error(t *testing.T) {
	v := IntegralAmountValidator{}
	cmd := ResolvedCommand{Command: Command{Amount: decimal.RequireFromString("100.5")}}

	err := v.Validate(context.Background(), nil, cmd, nil)

	assert.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestMinAmountValidator(t *testing.T) {

	v := MinAmountValidator{
		Min: decimal.NewFromInt(5),
	}

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(10),
		},
	}

	err := v.Validate(context.Background(), nil, cmd, nil)

	assert.NoError(t, err)
}

func TestMinAmountValidator_Error(t *testing.T) {

	v := MinAmountValidator{
		Min: decimal.NewFromInt(10),
	}

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(5),
		},
	}

	err := v.Validate(context.Background(), nil, cmd, nil)

	assert.Error(t, err)
}

func TestMaxAmountValidator(t *testing.T) {

	v := MaxAmountValidator{
		Max: decimal.NewFromInt(100),
	}

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(50),
		},
	}

	err := v.Validate(context.Background(), nil, cmd, nil)

	assert.NoError(t, err)
}

func TestMaxAmountValidator_Error(t *testing.T) {

	v := MaxAmountValidator{
		Max: decimal.NewFromInt(10),
	}

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(50),
		},
	}

	err := v.Validate(context.Background(), nil, cmd, nil)

	assert.Error(t, err)
}

func TestSufficientFundsValidator(t *testing.T) {

	accID := uuid.New()

	v := SufficientFundsValidator{
		AccountID: accID,
	}

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(10),
		},
	}

	balances := map[uuid.UUID]model.AccountBalance{
		accID: {
			AccountID: accID,
			Balance:   decimal.NewFromInt(100),
		},
	}

	err := v.Validate(context.Background(), nil, cmd, balances)

	assert.NoError(t, err)
}

func TestSufficientFundsValidator_AccountNotFound(t *testing.T) {

	v := SufficientFundsValidator{
		AccountID: uuid.New(),
	}

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(10),
		},
	}

	err := v.Validate(context.Background(), nil, cmd, map[uuid.UUID]model.AccountBalance{})

	assert.Error(t, err)
}

func TestSufficientFundsValidator_Insufficient(t *testing.T) {

	accID := uuid.New()

	v := SufficientFundsValidator{
		AccountID: accID,
	}

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
		},
	}

	balances := map[uuid.UUID]model.AccountBalance{
		accID: {
			AccountID: accID,
			Balance:   decimal.NewFromInt(10),
		},
	}

	err := v.Validate(context.Background(), nil, cmd, balances)

	assert.Error(t, err)
}

func TestNotSelfTransferValidator(t *testing.T) {

	a := uuid.New()
	b := uuid.New()

	v := NotSelfTransferValidator{
		A: a,
		B: b,
	}

	err := v.Validate(context.Background(), nil, ResolvedCommand{}, nil)

	assert.NoError(t, err)
}

func TestNotSelfTransferValidator_Error(t *testing.T) {

	a := uuid.New()

	v := NotSelfTransferValidator{
		A: a,
		B: a,
	}

	err := v.Validate(context.Background(), nil, ResolvedCommand{}, nil)

	assert.Error(t, err)
}

func TestFeeAmountValidator(t *testing.T) {

	v := FeeAmountValidator{}

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
			Metadata: map[string]any{
				"fee_amount": "10",
			},
		},
	}

	err := v.Validate(context.Background(), nil, cmd, nil)

	assert.NoError(t, err)
}

func TestFeeAmountValidator_InvalidDecimal(t *testing.T) {

	v := FeeAmountValidator{}

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
			Metadata: map[string]any{
				"fee_amount": "invalid",
			},
		},
	}

	err := v.Validate(context.Background(), nil, cmd, nil)

	assert.Error(t, err)
}

func TestFeeAmountValidator_Negative(t *testing.T) {

	v := FeeAmountValidator{}

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
			Metadata: map[string]any{
				"fee_amount": "-10",
			},
		},
	}

	err := v.Validate(context.Background(), nil, cmd, nil)

	assert.Error(t, err)
}

func TestFeeAmountValidator_TooLarge(t *testing.T) {

	v := FeeAmountValidator{}

	cmd := ResolvedCommand{
		Command: Command{
			Amount: decimal.NewFromInt(100),
			Metadata: map[string]any{
				"fee_amount": "100",
			},
		},
	}

	err := v.Validate(context.Background(), nil, cmd, nil)

	assert.Error(t, err)
}
