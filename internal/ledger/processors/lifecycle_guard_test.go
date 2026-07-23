package processors

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
)

// Unit coverage for docs/roadmap/archive/14 Task T2's lifecycle guard (decision K3):
// ValidateCommand requiring ReferenceID, and validateOriginalForClose's
// type/status/closed/amount checks. The race-proof guarantee itself
// (CloseOriginal's atomic UPDATE) can only be proven against real Postgres —
// see TestSchemaContract_ConcurrentReversal_NoDoubleClose in
// internal/ledger/schema_contract_test.go.

func TestRequireReferenceID_Missing_Rejected(t *testing.T) {
	err := requireReferenceID(Command{}, "withdraw_settle")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestRequireReferenceID_Present_OK(t *testing.T) {
	err := requireReferenceID(Command{ReferenceID: uuid.New()}, "withdraw_settle")
	assert.NoError(t, err)
}

// ─── ValidateCommand wiring for the 6 lifecycle processors ─────────────────

func TestValidateCommand_RequiresReferenceID(t *testing.T) {
	cases := []struct {
		name string
		vc   func(context.Context, Command) error
	}{
		{"withdraw_settle", (&WithdrawSettle{}).ValidateCommand},
		{"withdraw_cancel", (&WithdrawCancel{}).ValidateCommand},
		{"withdraw_pending_settle", (&WithdrawPendingSettle{}).ValidateCommand},
		{"withdraw_pending_cancel", (&WithdrawPendingCancel{}).ValidateCommand},
		{"escrow_release", (&EscrowRelease{}).ValidateCommand},
		{"escrow_refund", (&EscrowRefund{}).ValidateCommand},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.vc(context.Background(), Command{})
			require.Error(t, err, "%s must reject an empty ReferenceID", tc.name)
			assert.ErrorIs(t, err, apperror.ErrValidation)

			err = tc.vc(context.Background(), Command{ReferenceID: uuid.New()})
			assert.NoError(t, err, "%s must accept a non-nil ReferenceID", tc.name)
		})
	}
}

// ─── validateOriginalForClose ───────────────────────────────────────────────

func TestValidateOriginalForClose_TypeMismatch(t *testing.T) {
	txRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	refID := uuid.New()
	txRepo.EXPECT().GetHeader(gomock.Any(), gomock.Any(), refID).
		Return("withdraw_pending", "posted", decimal.NewFromInt(100), nil, nil)

	err := validateOriginalForClose(context.Background(), nil, txRepo, refID, "withdraw_initiate", decimal.NewFromInt(100))

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrOriginalTypeMismatch)
}

func TestValidateOriginalForClose_AlreadyClosed(t *testing.T) {
	txRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	refID := uuid.New()
	closedBy := uuid.New()
	txRepo.EXPECT().GetHeader(gomock.Any(), gomock.Any(), refID).
		Return("withdraw_initiate", "posted", decimal.NewFromInt(100), &closedBy, nil)

	err := validateOriginalForClose(context.Background(), nil, txRepo, refID, "withdraw_initiate", decimal.NewFromInt(100))

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrAlreadyClosed)
}

func TestValidateOriginalForClose_NotPosted(t *testing.T) {
	txRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	refID := uuid.New()
	txRepo.EXPECT().GetHeader(gomock.Any(), gomock.Any(), refID).
		Return("withdraw_initiate", "failed", decimal.NewFromInt(100), nil, nil)

	err := validateOriginalForClose(context.Background(), nil, txRepo, refID, "withdraw_initiate", decimal.NewFromInt(100))

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrNotReversible)
}

func TestValidateOriginalForClose_AmountMismatch(t *testing.T) {
	txRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	refID := uuid.New()
	txRepo.EXPECT().GetHeader(gomock.Any(), gomock.Any(), refID).
		Return("withdraw_initiate", "posted", decimal.NewFromInt(100), nil, nil)

	err := validateOriginalForClose(context.Background(), nil, txRepo, refID, "withdraw_initiate", decimal.NewFromInt(50))

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrLifecycleAmountMismatch)
}

func TestValidateOriginalForClose_AllChecksPass_OK(t *testing.T) {
	txRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	refID := uuid.New()
	txRepo.EXPECT().GetHeader(gomock.Any(), gomock.Any(), refID).
		Return("withdraw_initiate", "posted", decimal.NewFromInt(100), nil, nil)

	err := validateOriginalForClose(context.Background(), nil, txRepo, refID, "withdraw_initiate", decimal.NewFromInt(100))

	assert.NoError(t, err)
}

// ─── Reversal: reversal-of-reversal + already-closed ───────────────────────

func TestReversalValidate_OriginalIsReversal_Rejected(t *testing.T) {
	txRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	accRepo, ctrl2 := newMockAccountRepo(t)
	defer ctrl2.Finish()
	refID := uuid.New()
	txRepo.EXPECT().GetHeader(gomock.Any(), gomock.Any(), refID).
		Return("reversal", "posted", decimal.NewFromInt(100), nil, nil)

	p := NewReversal(txRepo, accRepo)
	err := p.Validate(context.Background(), nil, ResolvedCommand{Command: Command{ReferenceID: refID}}, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrNotReversible)
}

func TestReversalValidate_AlreadyClosed_Rejected(t *testing.T) {
	txRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	accRepo, ctrl2 := newMockAccountRepo(t)
	defer ctrl2.Finish()
	refID := uuid.New()
	closedBy := uuid.New()
	txRepo.EXPECT().GetHeader(gomock.Any(), gomock.Any(), refID).
		Return("money_in", "reversed", decimal.NewFromInt(100), &closedBy, nil)

	p := NewReversal(txRepo, accRepo)
	err := p.Validate(context.Background(), nil, ResolvedCommand{Command: Command{ReferenceID: refID}}, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrAlreadyReversed)
}

func TestReversalValidate_PostedNotClosed_OK(t *testing.T) {
	txRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	accRepo, ctrl2 := newMockAccountRepo(t)
	defer ctrl2.Finish()
	refID := uuid.New()
	txRepo.EXPECT().GetHeader(gomock.Any(), gomock.Any(), refID).
		Return("money_in", "posted", decimal.NewFromInt(100), nil, nil)

	p := NewReversal(txRepo, accRepo)
	err := p.Validate(context.Background(), nil, ResolvedCommand{Command: Command{ReferenceID: refID}}, nil)

	assert.NoError(t, err)
}
