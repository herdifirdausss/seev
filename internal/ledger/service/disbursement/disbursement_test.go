package disbursement

import (
	"context"
	"database/sql"
	"errors"
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
// goes through mocked repository methods (pola service/adjustments,
// service/schedule's own test files).
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

func newMockDisbursementRepo(t *testing.T) (*repository_mock.MockDisbursementRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockDisbursementRepository(ctrl), ctrl
}

func newMockTxRepo(t *testing.T) (*repository_mock.MockTransactionRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockTransactionRepository(ctrl), ctrl
}

// ─── Import validation ──────────────────────────────────────────────────────

func TestImport_EmptyRows_Rejected(t *testing.T) {
	repo, ctrl := newMockDisbursementRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	svc := New(mockDB{}, repo, txRepo, &fakePoster{})

	_, err := svc.Import(context.Background(), "f.csv", nil, "ops")
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestImport_TooManyRows_Rejected(t *testing.T) {
	repo, ctrl := newMockDisbursementRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	svc := New(mockDB{}, repo, txRepo, &fakePoster{})

	rows := make([]model.DisbursementImportRow, maxImportRows+1)
	for i := range rows {
		rows[i] = model.DisbursementImportRow{UserID: uuid.New(), Amount: decimal.NewFromInt(100)}
	}
	_, err := svc.Import(context.Background(), "f.csv", rows, "ops")
	assert.ErrorIs(t, err, apperror.ErrCSVTooManyRows)
}

func TestImport_EmptyUserID_Rejected(t *testing.T) {
	repo, ctrl := newMockDisbursementRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	svc := New(mockDB{}, repo, txRepo, &fakePoster{})

	rows := []model.DisbursementImportRow{{UserID: uuid.Nil, Amount: decimal.NewFromInt(100)}}
	_, err := svc.Import(context.Background(), "f.csv", rows, "ops")
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestImport_NonIntegralAmount_Rejected(t *testing.T) {
	repo, ctrl := newMockDisbursementRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	svc := New(mockDB{}, repo, txRepo, &fakePoster{})

	rows := []model.DisbursementImportRow{{UserID: uuid.New(), Amount: decimal.NewFromFloat(10.5)}}
	_, err := svc.Import(context.Background(), "f.csv", rows, "ops")
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestImport_MissingCreatedBy_Rejected(t *testing.T) {
	repo, ctrl := newMockDisbursementRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	svc := New(mockDB{}, repo, txRepo, &fakePoster{})

	rows := []model.DisbursementImportRow{{UserID: uuid.New(), Amount: decimal.NewFromInt(100)}}
	_, err := svc.Import(context.Background(), "f.csv", rows, "")
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestImport_Valid_Success(t *testing.T) {
	repo, ctrl := newMockDisbursementRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()
	repo.EXPECT().CreateBatchWithItems(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, batch model.DisbursementBatch, items []model.DisbursementItem) error {
			assert.Equal(t, "f.csv", batch.SourceFilename)
			assert.Equal(t, 2, batch.RowCount)
			assert.Len(t, items, 2)
			assert.Equal(t, 1, items[0].ItemNo)
			assert.Equal(t, 2, items[1].ItemNo)
			return nil
		})
	svc := New(mockDB{}, repo, txRepo, &fakePoster{})

	rows := []model.DisbursementImportRow{
		{UserID: uuid.New(), Amount: decimal.NewFromInt(100)},
		{UserID: uuid.New(), Amount: decimal.NewFromInt(200)},
	}
	id, err := svc.Import(context.Background(), "f.csv", rows, "ops")
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
}

// ─── Run ─────────────────────────────────────────────────────────────────

func TestRun_AllSucceed_MarksDoneAndCompleted(t *testing.T) {
	repo, ctrl := newMockDisbursementRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()

	batchID := uuid.New()
	item := model.DisbursementItem{ID: uuid.New(), BatchID: batchID, ItemNo: 1, UserID: uuid.New(), Amount: decimal.NewFromInt(100)}
	postedTxID := uuid.New()

	repo.EXPECT().GetBatch(gomock.Any(), batchID).Return(model.DisbursementBatch{ID: batchID, Status: "processing"}, nil)
	repo.EXPECT().ListItemsToProcess(gomock.Any(), batchID, false, gomock.Any()).Return([]model.DisbursementItem{item}, nil)
	txRepo.EXPECT().GetByIdempotencyKey(gomock.Any(), "batch:"+batchID.String()+":1", (*string)(nil)).
		Return(model.LedgerTransaction{ID: postedTxID}, nil)
	repo.EXPECT().MarkItemPosted(gomock.Any(), gomock.Any(), item.ID, postedTxID).Return(nil)
	repo.EXPECT().ListItemsToProcess(gomock.Any(), batchID, false, 1).Return(nil, nil)
	repo.EXPECT().GetCounts(gomock.Any(), batchID).Return(map[string]int{"posted": 1}, nil)
	repo.EXPECT().UpdateBatchStatus(gomock.Any(), gomock.Any(), batchID, "completed").Return(nil)

	svc := New(mockDB{}, repo, txRepo, &fakePoster{})
	result, err := svc.Run(context.Background(), batchID, false)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Processed)
	assert.Equal(t, 1, result.Posted)
	assert.Equal(t, 0, result.Failed)
	assert.True(t, result.Done)
}

func TestRun_BusinessFailure_MarksFailedAndCompletedWithErrors(t *testing.T) {
	repo, ctrl := newMockDisbursementRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()

	batchID := uuid.New()
	item := model.DisbursementItem{ID: uuid.New(), BatchID: batchID, ItemNo: 1, UserID: uuid.New(), Amount: decimal.NewFromInt(100)}

	repo.EXPECT().GetBatch(gomock.Any(), batchID).Return(model.DisbursementBatch{ID: batchID, Status: "processing"}, nil)
	repo.EXPECT().ListItemsToProcess(gomock.Any(), batchID, false, gomock.Any()).Return([]model.DisbursementItem{item}, nil)
	repo.EXPECT().MarkItemFailed(gomock.Any(), gomock.Any(), item.ID, gomock.Any()).Return(nil)
	repo.EXPECT().ListItemsToProcess(gomock.Any(), batchID, false, 1).Return(nil, nil)
	repo.EXPECT().GetCounts(gomock.Any(), batchID).Return(map[string]int{"failed": 1}, nil)
	repo.EXPECT().UpdateBatchStatus(gomock.Any(), gomock.Any(), batchID, "completed_with_errors").Return(nil)

	svc := New(mockDB{}, repo, txRepo, &fakePoster{err: apperror.NewBizErr(apperror.ErrAccountNotFound, "user has no cash account")})
	result, err := svc.Run(context.Background(), batchID, false)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Processed)
	assert.Equal(t, 0, result.Posted)
	assert.Equal(t, 1, result.Failed)
	assert.True(t, result.Done)
}

func TestRun_NotAllProcessed_NotDone(t *testing.T) {
	repo, ctrl := newMockDisbursementRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()

	batchID := uuid.New()
	item := model.DisbursementItem{ID: uuid.New(), BatchID: batchID, ItemNo: 1, UserID: uuid.New(), Amount: decimal.NewFromInt(100)}
	remainingItem := model.DisbursementItem{ID: uuid.New(), BatchID: batchID, ItemNo: 2, UserID: uuid.New(), Amount: decimal.NewFromInt(50)}
	postedTxID := uuid.New()

	repo.EXPECT().GetBatch(gomock.Any(), batchID).Return(model.DisbursementBatch{ID: batchID, Status: "processing"}, nil)
	repo.EXPECT().ListItemsToProcess(gomock.Any(), batchID, false, gomock.Any()).Return([]model.DisbursementItem{item}, nil)
	txRepo.EXPECT().GetByIdempotencyKey(gomock.Any(), gomock.Any(), (*string)(nil)).Return(model.LedgerTransaction{ID: postedTxID}, nil)
	repo.EXPECT().MarkItemPosted(gomock.Any(), gomock.Any(), item.ID, postedTxID).Return(nil)
	// Still one item left ('remaining') — batch must NOT be finalized.
	repo.EXPECT().ListItemsToProcess(gomock.Any(), batchID, false, 1).Return([]model.DisbursementItem{remainingItem}, nil)

	svc := New(mockDB{}, repo, txRepo, &fakePoster{})
	result, err := svc.Run(context.Background(), batchID, false)

	require.NoError(t, err)
	assert.False(t, result.Done, "batch must not be marked done while items remain")
}

func TestRun_UnknownBatch_ReturnsError(t *testing.T) {
	repo, ctrl := newMockDisbursementRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()

	batchID := uuid.New()
	repo.EXPECT().GetBatch(gomock.Any(), batchID).Return(model.DisbursementBatch{}, errors.New("not found"))

	svc := New(mockDB{}, repo, txRepo, &fakePoster{})
	_, err := svc.Run(context.Background(), batchID, false)
	assert.Error(t, err)
}

func TestWithMaxItemsPerRun_Overrides(t *testing.T) {
	repo, ctrl := newMockDisbursementRepo(t)
	defer ctrl.Finish()
	txRepo, ctrl2 := newMockTxRepo(t)
	defer ctrl2.Finish()

	batchID := uuid.New()
	repo.EXPECT().GetBatch(gomock.Any(), batchID).Return(model.DisbursementBatch{ID: batchID}, nil)
	repo.EXPECT().ListItemsToProcess(gomock.Any(), batchID, false, 3).Return(nil, nil)
	repo.EXPECT().ListItemsToProcess(gomock.Any(), batchID, false, 1).Return(nil, nil)
	repo.EXPECT().GetCounts(gomock.Any(), batchID).Return(map[string]int{}, nil)
	repo.EXPECT().UpdateBatchStatus(gomock.Any(), gomock.Any(), batchID, "completed").Return(nil)

	svc := New(mockDB{}, repo, txRepo, &fakePoster{}, WithMaxItemsPerRun(3))
	_, err := svc.Run(context.Background(), batchID, false)
	require.NoError(t, err)
}
