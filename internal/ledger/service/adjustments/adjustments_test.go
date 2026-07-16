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

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	repository_mock "github.com/herdifirdausss/seev/internal/ledger/repository"
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
	called bool
	err    error
}

func (f *fakePoster) Handle(_ context.Context, _ processors.Command) error {
	f.called = true
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

// ─── Approve: self-approval rejected (docs/plan/16 Task T1) ───────────────

func TestApprove_SelfApproval_Rejected(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	outbox, ctrl3 := newMockOutboxRepo(t)
	defer ctrl3.Finish()
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
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	outbox, ctrl3 := newMockOutboxRepo(t)
	defer ctrl3.Finish()
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
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	outbox, ctrl3 := newMockOutboxRepo(t)
	defer ctrl3.Finish()
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
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	outbox, ctrl3 := newMockOutboxRepo(t)
	defer ctrl3.Finish()
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
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	outbox, ctrl3 := newMockOutboxRepo(t)
	defer ctrl3.Finish()

	svc := New(mockDB{}, adjRepo, txRepo, outbox, &fakePoster{})
	_, err := svc.Create(context.Background(), "user-A", "money_in", decimal.NewFromInt(100), uuid.New(), nil, "reason")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_NonIntegralAmount_Rejected(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	outbox, ctrl3 := newMockOutboxRepo(t)
	defer ctrl3.Finish()

	svc := New(mockDB{}, adjRepo, txRepo, outbox, &fakePoster{})
	_, err := svc.Create(context.Background(), "user-A", "adjustment_credit", decimal.RequireFromString("100.5"), uuid.New(), nil, "reason")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_MissingReason_Rejected(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	outbox, ctrl3 := newMockOutboxRepo(t)
	defer ctrl3.Finish()

	svc := New(mockDB{}, adjRepo, txRepo, outbox, &fakePoster{})
	_, err := svc.Create(context.Background(), "user-A", "adjustment_credit", decimal.NewFromInt(100), uuid.New(), nil, "")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_Valid_Succeeds(t *testing.T) {
	adjRepo, ctrl := newMockAdjRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	outbox, ctrl3 := newMockOutboxRepo(t)
	defer ctrl3.Finish()

	adjRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), "user-A", gomock.Any(), "compensation").Return(nil)

	svc := New(mockDB{}, adjRepo, txRepo, outbox, &fakePoster{})
	id, err := svc.Create(context.Background(), "user-A", "adjustment_credit", decimal.NewFromInt(100), uuid.New(), nil, "compensation")

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
}
