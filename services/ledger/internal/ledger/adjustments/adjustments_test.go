package adjustments

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/processors"
	repository_mock "github.com/herdifirdausss/seev/services/ledger/internal/repository"
)

// mockDB.WithTx just calls fn(nil) — every write in this package's tests
// goes through mocked repository methods, so no real *sql.Tx is needed.
type mockDB struct{}

func (mockDB) WithTx(_ context.Context, _ *sql.TxOptions, fn func(*sql.Tx) error) error {
	return fn(nil)
}

// fakePoster is a hand-written test double for Poster — a single method
// doesn't earn a generated mock.
type fakePoster struct {
	called  bool
	err     error
	lastCmd processors.Command
}

func (f *fakePoster) Handle(_ context.Context, cmd processors.Command) error {
	f.called = true
	f.lastCmd = cmd
	return f.err
}

func newMockAdjRepo(t *testing.T) (*repository_mock.MockPendingAdjustmentRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockPendingAdjustmentRepository(ctrl), ctrl
}

func newMockTxRepo(t *testing.T) (*repository_mock.MockTransactionRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockTransactionRepository(ctrl), ctrl
}

func newMockOutboxRepo(t *testing.T) (*repository_mock.MockOutboxRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockOutboxRepository(ctrl), ctrl
}

// ─── Approve: self-approval rejected (docs/roadmap/archive/16 Task T1) ───────────────

func TestApprove_SelfApproval_Rejected(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, transactionRepoController := newMockTxRepo(t)
	defer transactionRepoController.Finish()
	outbox, outboxRepoController := newMockOutboxRepo(t)
	defer outboxRepoController.Finish()
	poster := &fakePoster{}

	id := uuid.New()
	adjRepo.EXPECT().GetByID(gomock.Any(), id).Return(model.PendingAdjustment{
		ID: id, RequestedBy: "user-A", Status: "pending",
	}, nil)
	// No MarkApproved expectation — the self-check must short-circuit
	// BEFORE any DB write is attempted.

	svc := New(mockDB{}, adjRepo, txRepo, outbox, poster)
	_, err := svc.Approve(context.Background(), id, "user-A")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrSelfApproval)
	assert.False(t, poster.called, "Post must never be called when self-approval is rejected")
}

func TestApprove_DifferentApprover_ProceedsPastSelfCheck(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, transactionRepoController := newMockTxRepo(t)
	defer transactionRepoController.Finish()
	outbox, outboxRepoController := newMockOutboxRepo(t)
	defer outboxRepoController.Finish()
	poster := &fakePoster{}

	id := uuid.New()
	payload := []byte(`{"type":"adjustment_credit","amount":"1000","user_id":"` + uuid.New().String() + `","metadata":{}}`)
	adjRepo.EXPECT().GetByID(gomock.Any(), id).Return(model.PendingAdjustment{
		ID: id, RequestedBy: "user-A", Status: "pending", CmdPayload: payload,
	}, nil)
	adjRepo.EXPECT().MarkApproved(gomock.Any(), gomock.Any(), id, "user-B").Return(int64(1), nil)
	adjRepo.EXPECT().MarkExecuted(gomock.Any(), gomock.Any(), id, gomock.Any()).Return(nil)
	txID := uuid.New()
	txRepo.EXPECT().GetByIdempotencyKey(gomock.Any(), "adj:"+id.String(), nil).
		Return(model.LedgerTransaction{ID: txID}, nil)
	outbox.EXPECT().InsertEvents(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	svc := New(mockDB{}, adjRepo, txRepo, outbox, poster)
	gotTxID, err := svc.Approve(context.Background(), id, "user-B")

	require.NoError(t, err)
	assert.True(t, poster.called, "a different approver must reach Post")
	assert.Equal(t, txID, gotTxID)
}

// ─── Approve: race loser (already decided) ─────────────────────────────────

func TestApprove_AlreadyDecided_Rejected(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, transactionRepoController := newMockTxRepo(t)
	defer transactionRepoController.Finish()
	outbox, outboxRepoController := newMockOutboxRepo(t)
	defer outboxRepoController.Finish()
	poster := &fakePoster{}

	id := uuid.New()
	adjRepo.EXPECT().GetByID(gomock.Any(), id).Return(model.PendingAdjustment{
		ID: id, RequestedBy: "user-A", Status: "approved",
	}, nil)
	// MarkApproved's atomic WHERE status='pending' matches nothing — the
	// row is already 'approved' by someone else.
	adjRepo.EXPECT().MarkApproved(gomock.Any(), gomock.Any(), id, "user-B").Return(int64(0), nil)

	svc := New(mockDB{}, adjRepo, txRepo, outbox, poster)
	_, err := svc.Approve(context.Background(), id, "user-B")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrAdjustmentAlreadyDecided)
	assert.False(t, poster.called, "Post must never run for the losing side of the race")
}

// ─── Approve: Post failure marks 'failed', not back to 'pending' ──────────

func TestApprove_PostFails_MarksFailedNotPending(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, transactionRepoController := newMockTxRepo(t)
	defer transactionRepoController.Finish()
	outbox, outboxRepoController := newMockOutboxRepo(t)
	defer outboxRepoController.Finish()
	poster := &fakePoster{err: assertAnError{}}

	id := uuid.New()
	payload := []byte(`{"type":"adjustment_debit","amount":"500","user_id":"` + uuid.New().String() + `","metadata":{}}`)
	adjRepo.EXPECT().GetByID(gomock.Any(), id).Return(model.PendingAdjustment{
		ID: id, RequestedBy: "user-A", Status: "pending", CmdPayload: payload,
	}, nil)
	adjRepo.EXPECT().MarkApproved(gomock.Any(), gomock.Any(), id, "user-B").Return(int64(1), nil)
	adjRepo.EXPECT().MarkFailed(gomock.Any(), gomock.Any(), id, gomock.Any()).Return(nil)

	svc := New(mockDB{}, adjRepo, txRepo, outbox, poster)
	_, err := svc.Approve(context.Background(), id, "user-B")

	require.Error(t, err)
	assert.True(t, poster.called)
}

type assertAnError struct{}

func (assertAnError) Error() string { return "post failed" }

// ─── Create: validation ────────────────────────────────────────────────────

func TestCreate_InvalidType_Rejected(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, transactionRepoController := newMockTxRepo(t)
	defer transactionRepoController.Finish()
	outbox, outboxRepoController := newMockOutboxRepo(t)
	defer outboxRepoController.Finish()

	svc := New(mockDB{}, adjRepo, txRepo, outbox, &fakePoster{})
	_, err := svc.Create(context.Background(), "user-A", "money_in", decimal.NewFromInt(100), uuid.New(), uuid.Nil, nil, "reason")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_NonIntegralAmount_Rejected(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, transactionRepoController := newMockTxRepo(t)
	defer transactionRepoController.Finish()
	outbox, outboxRepoController := newMockOutboxRepo(t)
	defer outboxRepoController.Finish()

	svc := New(mockDB{}, adjRepo, txRepo, outbox, &fakePoster{})
	_, err := svc.Create(context.Background(), "user-A", "adjustment_credit", decimal.RequireFromString("100.5"), uuid.New(), uuid.Nil, nil, "reason")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_MissingReason_Rejected(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, transactionRepoController := newMockTxRepo(t)
	defer transactionRepoController.Finish()
	outbox, outboxRepoController := newMockOutboxRepo(t)
	defer outboxRepoController.Finish()

	svc := New(mockDB{}, adjRepo, txRepo, outbox, &fakePoster{})
	_, err := svc.Create(context.Background(), "user-A", "adjustment_credit", decimal.NewFromInt(100), uuid.New(), uuid.Nil, nil, "")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_Valid_Succeeds(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, transactionRepoController := newMockTxRepo(t)
	defer transactionRepoController.Finish()
	outbox, outboxRepoController := newMockOutboxRepo(t)
	defer outboxRepoController.Finish()

	adjRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), "user-A", gomock.Any(), "compensation").Return(nil)

	svc := New(mockDB{}, adjRepo, txRepo, outbox, &fakePoster{})
	id, err := svc.Create(context.Background(), "user-A", "adjustment_credit", decimal.NewFromInt(100), uuid.New(), uuid.Nil, nil, "compensation")

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
}

// ─── reversal/chargeback/freeze_confiscate: security audit finding ─────────
// These three used to be directly postable with a single admin JWT
// (services/ledger/internal/transport/http.go's old adminOnlyTypes) — now routed
// through the same maker-checker path as adjustment_credit/debit.

func TestCreate_Reversal_MissingReferenceID_Rejected(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, transactionRepoController := newMockTxRepo(t)
	defer transactionRepoController.Finish()
	outbox, outboxRepoController := newMockOutboxRepo(t)
	defer outboxRepoController.Finish()

	svc := New(mockDB{}, adjRepo, txRepo, outbox, &fakePoster{})
	// referenceID left uuid.Nil — reversal has no UserID to fall back to.
	_, err := svc.Create(context.Background(), "user-A", "reversal", decimal.NewFromInt(1000), uuid.Nil, uuid.Nil, nil, "undo double post")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_Reversal_WithReferenceID_Succeeds(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, transactionRepoController := newMockTxRepo(t)
	defer transactionRepoController.Finish()
	outbox, outboxRepoController := newMockOutboxRepo(t)
	defer outboxRepoController.Finish()

	adjRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), "user-A", gomock.Any(), "undo double post").Return(nil)

	svc := New(mockDB{}, adjRepo, txRepo, outbox, &fakePoster{})
	originalTxID := uuid.New()
	id, err := svc.Create(context.Background(), "user-A", "reversal", decimal.NewFromInt(1000), uuid.Nil, originalTxID, nil, "undo double post")

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
}

func TestCreate_ChargebackAndFreezeConfiscate_RequireUserID(t *testing.T) {
	for _, adjType := range []string{"chargeback", "freeze_confiscate"} {
		t.Run(adjType, func(t *testing.T) {
			adjRepo, ctrl := newMockAdjRepo(t)
			defer ctrl.Finish()
			txRepo, transactionRepoController := newMockTxRepo(t)
			defer transactionRepoController.Finish()
			outbox, outboxRepoController := newMockOutboxRepo(t)
			defer outboxRepoController.Finish()

			svc := New(mockDB{}, adjRepo, txRepo, outbox, &fakePoster{})
			_, err := svc.Create(context.Background(), "user-A", adjType, decimal.NewFromInt(1000), uuid.Nil, uuid.Nil, nil, "reason")

			require.Error(t, err)
			assert.ErrorIs(t, err, apperror.ErrValidation)
		})
	}
}

// TestApprove_Reversal_ThreadsReferenceIDIntoPostedCommand proves Approve
// carries a reversal's stored ReferenceID through into the actual posted
// processors.Command — without this, Reversal.ResolveAccounts (which reads
// ONLY cmd.ReferenceID, never cmd.UserID) would always fail validation.
func TestApprove_Reversal_ThreadsReferenceIDIntoPostedCommand(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, transactionRepoController := newMockTxRepo(t)
	defer transactionRepoController.Finish()
	outbox, outboxRepoController := newMockOutboxRepo(t)
	defer outboxRepoController.Finish()
	poster := &fakePoster{}

	id := uuid.New()
	originalTxID := uuid.New()
	payload := []byte(`{"type":"reversal","amount":"1000","user_id":"00000000-0000-0000-0000-000000000000","metadata":{},"reference_id":"` + originalTxID.String() + `"}`)
	adjRepo.EXPECT().GetByID(gomock.Any(), id).Return(model.PendingAdjustment{
		ID: id, RequestedBy: "user-A", Status: "pending", CmdPayload: payload,
	}, nil)
	adjRepo.EXPECT().MarkApproved(gomock.Any(), gomock.Any(), id, "user-B").Return(int64(1), nil)
	adjRepo.EXPECT().MarkExecuted(gomock.Any(), gomock.Any(), id, gomock.Any()).Return(nil)
	txID := uuid.New()
	txRepo.EXPECT().GetByIdempotencyKey(gomock.Any(), "adj:"+id.String(), nil).
		Return(model.LedgerTransaction{ID: txID}, nil)
	outbox.EXPECT().InsertEvents(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	svc := New(mockDB{}, adjRepo, txRepo, outbox, poster)
	_, err := svc.Approve(context.Background(), id, "user-B")

	require.NoError(t, err)
	assert.Equal(t, originalTxID, poster.lastCmd.ReferenceID)
	assert.Equal(t, "reversal", poster.lastCmd.Type)
}
