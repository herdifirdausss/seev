package schedule

import (
	"context"
	"database/sql"
	"testing"
	"time"

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
// goes through mocked repository methods, so no real *sql.Tx is needed
// (pola service/adjustments' own test file).
type mockDB struct{}

func (mockDB) WithTx(_ context.Context, _ *sql.TxOptions, fn func(*sql.Tx) error) error {
	return fn(nil)
}

// fakePoster is a hand-written test double for Poster.
type fakePoster struct {
	err error
}

func (f *fakePoster) Handle(_ context.Context, _ processors.Command) error {
	return f.err
}

func newMockScheduleRepo(t *testing.T) (*repository_mock.MockScheduledTransactionRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockScheduledTransactionRepository(ctrl), ctrl
}

// ─── Create validation ──────────────────────────────────────────────────────

func TestCreate_DisallowedType_Rejected(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	svc := New(mockDB{}, repo, &fakePoster{}, nil)

	_, err := svc.Create(context.Background(), uuid.New(), "money_in", decimal.NewFromInt(100),
		uuid.New(), "", nil, "daily", time.Now(), nil, "user")

	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_MonthlyWithoutDayOfMonth_Rejected(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	svc := New(mockDB{}, repo, &fakePoster{}, nil)

	_, err := svc.Create(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(100),
		uuid.New(), "", nil, "monthly", time.Now(), nil, "user")

	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_MonthlyDayOfMonth29_Rejected(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	svc := New(mockDB{}, repo, &fakePoster{}, nil)

	day := 29
	_, err := svc.Create(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(100),
		uuid.New(), "", nil, "monthly", time.Now(), &day, "user")

	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_DayOfMonthOnNonMonthly_Rejected(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	svc := New(mockDB{}, repo, &fakePoster{}, nil)

	day := 15
	_, err := svc.Create(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(100),
		uuid.New(), "", nil, "daily", time.Now(), &day, "user")

	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_TransferP2P_MissingTargetUser_Rejected(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	svc := New(mockDB{}, repo, &fakePoster{}, nil)

	_, err := svc.Create(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(100),
		uuid.Nil, "", nil, "daily", time.Now(), nil, "user")

	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_TransferP2P_SelfTarget_Rejected(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	svc := New(mockDB{}, repo, &fakePoster{}, nil)

	userID := uuid.New()
	_, err := svc.Create(context.Background(), userID, "transfer_p2p", decimal.NewFromInt(100),
		userID, "", nil, "daily", time.Now(), nil, "user")

	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_TransferPocket_MissingPocketCode_Rejected(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	svc := New(mockDB{}, repo, &fakePoster{}, nil)

	_, err := svc.Create(context.Background(), uuid.New(), "transfer_pocket", decimal.NewFromInt(100),
		uuid.Nil, "", nil, "daily", time.Now(), nil, "user")

	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreate_ValidDaily_Success(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	repo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "daily", gomock.Any(), (*int)(nil), "user").Return(nil)
	svc := New(mockDB{}, repo, &fakePoster{}, nil)

	id, err := svc.Create(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(100),
		uuid.New(), "", nil, "daily", time.Now(), nil, "user")

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
}

func TestCreate_ValidMonthly_Success(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	day := 5
	repo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "monthly", gomock.Any(), &day, "user").Return(nil)
	svc := New(mockDB{}, repo, &fakePoster{}, nil)

	id, err := svc.Create(context.Background(), uuid.New(), "transfer_pocket", decimal.NewFromInt(100),
		uuid.Nil, "savings", nil, "monthly", time.Now(), &day, "user")

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
}

// ─── RunDue: business failure keeps a recurring schedule active ────────────

func TestRunDue_BusinessFailure_Recurring_StaysActive(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	asOf := time.Now()
	row := model.ScheduledTransaction{
		ID: uuid.New(), UserID: uuid.New(), ScheduleKind: "daily",
		CmdPayload: []byte(`{"type":"transfer_p2p","amount":"100","target_user_id":"` + uuid.New().String() + `"}`),
	}
	repo.EXPECT().ListDue(gomock.Any(), asOf).Return([]model.ScheduledTransaction{row}, nil)
	// terminal=false for a 'daily' schedule — recurring must stay 'active'.
	repo.EXPECT().MarkBusinessFailure(gomock.Any(), gomock.Any(), row.ID, gomock.Any(), false).Return(nil)

	svc := New(mockDB{}, repo, &fakePoster{err: apperror.NewBizErr(apperror.ErrInsufficientFunds, "insufficient funds")}, nil)
	executed, failed, err := svc.RunDue(context.Background(), asOf)

	require.NoError(t, err)
	assert.Equal(t, 0, executed)
	assert.Equal(t, 1, failed)
}

func TestRunDue_BusinessFailure_Once_MarkedTerminal(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	asOf := time.Now()
	row := model.ScheduledTransaction{
		ID: uuid.New(), UserID: uuid.New(), ScheduleKind: "once",
		CmdPayload: []byte(`{"type":"transfer_p2p","amount":"100","target_user_id":"` + uuid.New().String() + `"}`),
	}
	repo.EXPECT().ListDue(gomock.Any(), asOf).Return([]model.ScheduledTransaction{row}, nil)
	// terminal=true for a 'once' schedule — a permanent business failure ends it.
	repo.EXPECT().MarkBusinessFailure(gomock.Any(), gomock.Any(), row.ID, gomock.Any(), true).Return(nil)

	svc := New(mockDB{}, repo, &fakePoster{err: apperror.NewBizErr(apperror.ErrAccountSuspended, "suspended")}, nil)
	executed, failed, err := svc.RunDue(context.Background(), asOf)

	require.NoError(t, err)
	assert.Equal(t, 0, executed)
	assert.Equal(t, 1, failed)
}

func TestRunDue_InfraFailure_RowUntouched(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	asOf := time.Now()
	row := model.ScheduledTransaction{
		ID: uuid.New(), UserID: uuid.New(), ScheduleKind: "daily",
		CmdPayload: []byte(`{"type":"transfer_p2p","amount":"100","target_user_id":"` + uuid.New().String() + `"}`),
	}
	repo.EXPECT().ListDue(gomock.Any(), asOf).Return([]model.ScheduledTransaction{row}, nil)
	// NO MarkSuccess/MarkBusinessFailure call expected — an infra error must
	// leave the row completely untouched (docs/plan/19 Task T1 step 3).

	svc := New(mockDB{}, repo, &fakePoster{err: assertPlainError{}}, nil)
	executed, failed, err := svc.RunDue(context.Background(), asOf)

	require.NoError(t, err)
	assert.Equal(t, 0, executed)
	assert.Equal(t, 1, failed)
}

type assertPlainError struct{}

func (assertPlainError) Error() string { return "connection refused" }

func TestRunDue_Success_MarksRunAndFinishesOnce(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	asOf := time.Now()
	row := model.ScheduledTransaction{
		ID: uuid.New(), UserID: uuid.New(), ScheduleKind: "once",
		CmdPayload: []byte(`{"type":"transfer_p2p","amount":"100","target_user_id":"` + uuid.New().String() + `"}`),
	}
	repo.EXPECT().ListDue(gomock.Any(), asOf).Return([]model.ScheduledTransaction{row}, nil)
	repo.EXPECT().MarkSuccess(gomock.Any(), gomock.Any(), row.ID, asOf, true).Return(nil)

	svc := New(mockDB{}, repo, &fakePoster{}, nil)
	executed, failed, err := svc.RunDue(context.Background(), asOf)

	require.NoError(t, err)
	assert.Equal(t, 1, executed)
	assert.Equal(t, 0, failed)
}

func TestRunDue_AlreadyPosted_TreatedAsSuccess(t *testing.T) {
	// The crash-window case: a prior run posted successfully but crashed
	// before last_run_date was updated. RunDue's retry must treat
	// ErrAlreadyPosted as success and still write last_run_date.
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	asOf := time.Now()
	row := model.ScheduledTransaction{
		ID: uuid.New(), UserID: uuid.New(), ScheduleKind: "daily",
		CmdPayload: []byte(`{"type":"transfer_p2p","amount":"100","target_user_id":"` + uuid.New().String() + `"}`),
	}
	repo.EXPECT().ListDue(gomock.Any(), asOf).Return([]model.ScheduledTransaction{row}, nil)
	repo.EXPECT().MarkSuccess(gomock.Any(), gomock.Any(), row.ID, asOf, false).Return(nil)

	svc := New(mockDB{}, repo, &fakePoster{err: apperror.ErrAlreadyPosted}, nil)
	executed, failed, err := svc.RunDue(context.Background(), asOf)

	require.NoError(t, err)
	assert.Equal(t, 1, executed)
	assert.Equal(t, 0, failed)
}

// ─── Pause/Resume/Cancel ownership ──────────────────────────────────────────

func TestPause_NotOwner_Rejected(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	id := uuid.New()
	repo.EXPECT().GetByID(gomock.Any(), id).Return(model.ScheduledTransaction{ID: id, UserID: uuid.New()}, nil)

	svc := New(mockDB{}, repo, &fakePoster{}, nil)
	err := svc.Pause(context.Background(), id, uuid.New())

	assert.ErrorIs(t, err, apperror.ErrScheduledTransactionNotOwned)
}

func TestPause_Owner_Success(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	id := uuid.New()
	userID := uuid.New()
	repo.EXPECT().GetByID(gomock.Any(), id).Return(model.ScheduledTransaction{ID: id, UserID: userID}, nil)
	repo.EXPECT().Pause(gomock.Any(), gomock.Any(), id).Return(int64(1), nil)

	svc := New(mockDB{}, repo, &fakePoster{}, nil)
	err := svc.Pause(context.Background(), id, userID)

	assert.NoError(t, err)
}

func TestPause_AlreadyTerminal_Rejected(t *testing.T) {
	repo, ctrl := newMockScheduleRepo(t)
	defer ctrl.Finish()
	id := uuid.New()
	userID := uuid.New()
	repo.EXPECT().GetByID(gomock.Any(), id).Return(model.ScheduledTransaction{ID: id, UserID: userID}, nil)
	repo.EXPECT().Pause(gomock.Any(), gomock.Any(), id).Return(int64(0), nil)

	svc := New(mockDB{}, repo, &fakePoster{}, nil)
	err := svc.Pause(context.Background(), id, userID)

	assert.ErrorIs(t, err, apperror.ErrScheduledTransactionAlreadyTerminal)
}
