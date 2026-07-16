package processors

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	repository_mock "github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func newMockAccountRepo(t *testing.T) (*repository_mock.MockAccountRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockAccountRepository(ctrl), ctrl
}

func newMockTransactionRepo(t *testing.T) (*repository_mock.MockTransactionRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockTransactionRepository(ctrl), ctrl
}

func newMockTxProcessor(t *testing.T) (*MockTxProcessor, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return NewMockTxProcessor(ctrl), ctrl
}

func TestResolveInlineFee_NoMetadata(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()

	cmd := Command{
		Metadata: map[string]any{},
	}

	id, fee, err := resolveInlineFee(context.Background(), repo, cmd, "IDR")

	assert.NoError(t, err)
	assert.Equal(t, uuid.Nil, id)
	assert.True(t, fee.IsZero())
}

func TestResolveInlineFee_InvalidDecimal(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()

	cmd := Command{
		Metadata: map[string]any{
			"fee_amount": "invalid",
		},
	}

	_, _, err := resolveInlineFee(context.Background(), repo, cmd, "IDR")

	assert.Error(t, err)
}

func TestResolveInlineFee_Negative(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()

	cmd := Command{
		Metadata: map[string]any{
			"fee_amount": "-10",
		},
	}

	_, _, err := resolveInlineFee(context.Background(), repo, cmd, "IDR")

	assert.Error(t, err)
}

func TestResolveInlineFee_NoGateway(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()

	cmd := Command{
		Metadata: map[string]any{
			"fee_amount": "10",
		},
	}

	_, _, err := resolveInlineFee(context.Background(), repo, cmd, "IDR")

	assert.Error(t, err)
}

func TestResolveInlineFee_Success(t *testing.T) {
	id := uuid.New()

	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()

	repo.EXPECT().GetSystemAccountID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(id, nil)

	cmd := Command{
		Metadata: map[string]any{
			"fee_amount":  "10",
			"fee_gateway": "bca",
		},
	}

	gotID, fee, err := resolveInlineFee(context.Background(), repo, cmd, "IDR")

	assert.NoError(t, err)
	assert.Equal(t, id, gotID)
	assert.True(t, fee.Equal(decimal.NewFromInt(10)))
}

func TestResolveInlineFee_RepoError(t *testing.T) {
	repo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()

	repo.EXPECT().GetSystemAccountID(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(uuid.Nil, errors.New("db error"))

	cmd := Command{
		Metadata: map[string]any{
			"fee_amount":  "10",
			"fee_gateway": "bca",
		},
	}

	_, _, err := resolveInlineFee(context.Background(), repo, cmd, "IDR")

	assert.Error(t, err)
}

func TestRequireGateway(t *testing.T) {
	cmd := Command{
		Metadata: map[string]any{
			"gateway": "bca",
		},
	}

	gw, err := requireGateway(cmd, "money_in")

	assert.NoError(t, err)
	assert.Equal(t, "bca", gw)
}

func TestRequireGateway_Error(t *testing.T) {
	cmd := Command{
		Metadata: map[string]any{},
	}

	_, err := requireGateway(cmd, "money_in")

	assert.Error(t, err)
}

func TestHasFee_NoFee(t *testing.T) {
	cmd := ResolvedCommand{
		Command: Command{
			Metadata: map[string]any{},
		},
		AccountIDs: []uuid.UUID{uuid.New(), uuid.New()},
	}

	_, _, ok := hasFee(cmd)

	assert.False(t, ok)
}

func TestHasFee_Success(t *testing.T) {
	feeID := uuid.New()

	cmd := ResolvedCommand{
		Command: Command{
			Metadata: map[string]any{
				"fee_amount": "10",
			},
		},
		AccountIDs: []uuid.UUID{
			uuid.New(),
			uuid.New(),
			feeID,
		},
	}

	id, fee, ok := hasFee(cmd)

	assert.True(t, ok)
	assert.Equal(t, feeID, id)
	assert.True(t, fee.Equal(decimal.NewFromInt(10)))
}

func TestNewRegistry_Get(t *testing.T) {
	p, ctrl := newMockTxProcessor(t)
	defer ctrl.Finish()

	p.EXPECT().Type().Return("test")

	reg := NewRegistry(p)

	got, err := reg.Get("test")

	assert.NoError(t, err)
	assert.Equal(t, p, got)
}

func TestNewRegistry_Get_NotFound(t *testing.T) {
	reg := NewRegistry()

	_, err := reg.Get("unknown")

	assert.Error(t, err)
}

func TestNewRegistry_DuplicatePanic(t *testing.T) {
	p, ctrl := newMockTxProcessor(t)
	defer ctrl.Finish()
	proc, ctrlProc := newMockTxProcessor(t)
	defer ctrlProc.Finish()
	p.EXPECT().Type().Return("duplicate")
	proc.EXPECT().Type().Return("duplicate")
	assert.Panics(t, func() {
		NewRegistry(
			p, proc,
		)
	})
}

func TestNewDefaultRegistry(t *testing.T) {
	accrepo, ctrl := newMockAccountRepo(t)
	defer ctrl.Finish()

	txRepo, ctrlTx := newMockTransactionRepo(t)
	defer ctrlTx.Finish()

	reg := NewDefaultRegistry(accrepo, txRepo)

	assert.NotNil(t, reg)

	// sample processors
	_, err := reg.Get("money_in")
	assert.NoError(t, err)

	_, err = reg.Get("transfer_p2p")
	assert.NoError(t, err)
}
