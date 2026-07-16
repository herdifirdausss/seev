package recon

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
	repository_mock "github.com/herdifirdausss/seev/internal/ledger/repository"
)

// mockDB.WithTx just calls fn(nil) — every write in this package's tests
// goes through mocked repository methods, so no real *sql.Tx is needed.
type mockDB struct{}

func (mockDB) WithTx(_ context.Context, _ *sql.TxOptions, fn func(*sql.Tx) error) error {
	return fn(nil)
}

// fakeAdjustmentCreator is a hand-written test double for AdjustmentCreator
// — a single method doesn't earn a generated mock.
type fakeAdjustmentCreator struct {
	called       bool
	returnedID   uuid.UUID
	err          error
	lastAdjType  string
	lastAmount   decimal.Decimal
	lastMetadata map[string]any
}

func (f *fakeAdjustmentCreator) Create(_ context.Context, _, adjType string, amount decimal.Decimal, _ uuid.UUID, metadata map[string]any, _ string) (uuid.UUID, error) {
	f.called = true
	f.lastAdjType = adjType
	f.lastAmount = amount
	f.lastMetadata = metadata
	if f.err != nil {
		return uuid.Nil, f.err
	}
	if f.returnedID == uuid.Nil {
		f.returnedID = uuid.New()
	}
	return f.returnedID, nil
}

func newMockReconRepo(t *testing.T) (*repository_mock.MockReconRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockReconRepository(ctrl), ctrl
}

// ─── ImportBatch: validation ────────────────────────────────────────────────

func TestImportBatch_UnknownGateway_Rejected(t *testing.T) {
	repo, ctrl := newMockReconRepo(t)
	defer ctrl.Finish()

	svc := New(mockDB{}, repo, &fakeAdjustmentCreator{})
	_, err := svc.ImportBatch(context.Background(), "unknown-gw", time.Now(), "f.csv",
		[]ImportRow{{ExternalRef: "r1", Amount: decimal.NewFromInt(100)}}, "ops-1")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestImportBatch_EmptyRows_Rejected(t *testing.T) {
	repo, ctrl := newMockReconRepo(t)
	defer ctrl.Finish()

	svc := New(mockDB{}, repo, &fakeAdjustmentCreator{})
	_, err := svc.ImportBatch(context.Background(), "bca", time.Now(), "f.csv", nil, "ops-1")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestImportBatch_TooManyRows_Rejected(t *testing.T) {
	repo, ctrl := newMockReconRepo(t)
	defer ctrl.Finish()

	rows := make([]ImportRow, maxImportRows+1)
	for i := range rows {
		rows[i] = ImportRow{ExternalRef: uuid.NewString(), Amount: decimal.NewFromInt(100)}
	}

	svc := New(mockDB{}, repo, &fakeAdjustmentCreator{})
	_, err := svc.ImportBatch(context.Background(), "bca", time.Now(), "f.csv", rows, "ops-1")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrCSVTooManyRows)
}

func TestImportBatch_DuplicateExternalRef_Rejected(t *testing.T) {
	repo, ctrl := newMockReconRepo(t)
	defer ctrl.Finish()

	svc := New(mockDB{}, repo, &fakeAdjustmentCreator{})
	_, err := svc.ImportBatch(context.Background(), "bca", time.Now(), "f.csv", []ImportRow{
		{ExternalRef: "dup", Amount: decimal.NewFromInt(100)},
		{ExternalRef: "dup", Amount: decimal.NewFromInt(200)},
	}, "ops-1")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestImportBatch_NonIntegralAmount_Rejected(t *testing.T) {
	repo, ctrl := newMockReconRepo(t)
	defer ctrl.Finish()

	svc := New(mockDB{}, repo, &fakeAdjustmentCreator{})
	_, err := svc.ImportBatch(context.Background(), "bca", time.Now(), "f.csv", []ImportRow{
		{ExternalRef: "r1", Amount: decimal.RequireFromString("100.5")},
	}, "ops-1")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestImportBatch_MissingCreatedBy_Rejected(t *testing.T) {
	repo, ctrl := newMockReconRepo(t)
	defer ctrl.Finish()

	svc := New(mockDB{}, repo, &fakeAdjustmentCreator{})
	_, err := svc.ImportBatch(context.Background(), "bca", time.Now(), "f.csv", []ImportRow{
		{ExternalRef: "r1", Amount: decimal.NewFromInt(100)},
	}, "")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestImportBatch_Valid_Succeeds(t *testing.T) {
	repo, ctrl := newMockReconRepo(t)
	defer ctrl.Finish()

	reportDate := time.Now()
	repo.EXPECT().CreateBatch(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().InsertItems(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().RunMatcher(gomock.Any(), gomock.Any(), gomock.Any(), "bca", reportDate).Return(nil)
	repo.EXPECT().UpdateBatchStatus(gomock.Any(), gomock.Any(), gomock.Any(), "completed").Return(nil)

	svc := New(mockDB{}, repo, &fakeAdjustmentCreator{})
	id, err := svc.ImportBatch(context.Background(), "bca", reportDate, "f.csv", []ImportRow{
		{ExternalRef: "r1", Amount: decimal.NewFromInt(100)},
	}, "ops-1")

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
}

// ─── ResolveItem ─────────────────────────────────────────────────────────────

func TestResolveItem_InvalidType_Rejected(t *testing.T) {
	repo, ctrl := newMockReconRepo(t)
	defer ctrl.Finish()

	svc := New(mockDB{}, repo, &fakeAdjustmentCreator{})
	_, err := svc.ResolveItem(context.Background(), uuid.New(), "ops-1", "adjustment_credit", decimal.NewFromInt(100), "reason")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestResolveItem_AlreadyResolved_Rejected(t *testing.T) {
	repo, ctrl := newMockReconRepo(t)
	defer ctrl.Finish()

	itemID := uuid.New()
	adjID := uuid.New()
	repo.EXPECT().GetItem(gomock.Any(), itemID).Return(model.ReconItem{
		ID: itemID, ResolvedByAdjustmentID: &adjID,
	}, nil)

	adj := &fakeAdjustmentCreator{}
	svc := New(mockDB{}, repo, adj)
	_, err := svc.ResolveItem(context.Background(), itemID, "ops-1", "adjustment_suspense_credit", decimal.NewFromInt(100), "reason")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrReconItemAlreadyResolved)
	assert.False(t, adj.called, "must not create an adjustment for an already-resolved item")
}

func TestResolveItem_ConcurrentResolve_OrphansAdjustment(t *testing.T) {
	repo, ctrl := newMockReconRepo(t)
	defer ctrl.Finish()

	itemID := uuid.New()
	batchID := uuid.New()
	repo.EXPECT().GetItem(gomock.Any(), itemID).Return(model.ReconItem{
		ID: itemID, BatchID: batchID, ExternalRef: "r1", Amount: decimal.NewFromInt(100), MatchStatus: "missing_internal",
	}, nil)
	repo.EXPECT().GetBatch(gomock.Any(), batchID).Return(model.ReconBatch{ID: batchID, Gateway: "bca"}, nil)
	// Someone else resolved the item between GetItem and MarkItemResolved —
	// the atomic guard reports 0 rows affected.
	repo.EXPECT().MarkItemResolved(gomock.Any(), gomock.Any(), itemID, gomock.Any()).Return(int64(0), nil)

	adj := &fakeAdjustmentCreator{}
	svc := New(mockDB{}, repo, adj)
	_, err := svc.ResolveItem(context.Background(), itemID, "ops-1", "adjustment_suspense_credit", decimal.Zero, "")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrReconItemAlreadyResolved)
	assert.True(t, adj.called, "the losing side still creates a (now orphaned) pending adjustment")
}

func TestResolveItem_Valid_Succeeds(t *testing.T) {
	repo, ctrl := newMockReconRepo(t)
	defer ctrl.Finish()

	itemID := uuid.New()
	batchID := uuid.New()
	repo.EXPECT().GetItem(gomock.Any(), itemID).Return(model.ReconItem{
		ID: itemID, BatchID: batchID, ExternalRef: "r1", Amount: decimal.NewFromInt(5000), MatchStatus: "missing_internal",
	}, nil)
	repo.EXPECT().GetBatch(gomock.Any(), batchID).Return(model.ReconBatch{ID: batchID, Gateway: "gopay"}, nil)
	repo.EXPECT().MarkItemResolved(gomock.Any(), gomock.Any(), itemID, gomock.Any()).Return(int64(1), nil)

	adj := &fakeAdjustmentCreator{}
	svc := New(mockDB{}, repo, adj)

	// amount=0 means "use the item's own amount".
	id, err := svc.ResolveItem(context.Background(), itemID, "ops-1", "adjustment_suspense_credit", decimal.Zero, "")

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
	assert.True(t, adj.called)
	assert.True(t, adj.lastAmount.Equal(decimal.NewFromInt(5000)), "must default to the recon item's own amount")
	assert.Equal(t, "gopay", adj.lastMetadata["gateway"])
}
