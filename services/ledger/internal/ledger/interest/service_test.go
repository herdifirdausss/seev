package interest

import (
	"context"
	"database/sql"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/processors"
	repository_mock "github.com/herdifirdausss/seev/services/ledger/internal/repository"
)

// mockDB.WithTx just calls fn(nil) — every write in this package's tests goes
// through the mocked repository, so no real *sql.Tx is needed (same pattern
// as schedule/schedule_test.go).
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

func newMockInterestRepo(t *testing.T) (*repository_mock.MockInterestRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockInterestRepository(ctrl), ctrl
}

func denominatorString() string {
	return BigIntString(big.NewInt(DailyDenominator))
}

// ─── PreviewPeriodClose ─────────────────────────────────────────────────────

func TestPreviewPeriodClose_ReadyWhenInventoryComplete(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	periodID := uuid.New()
	enrollmentID := uuid.New()
	snapshotID := uuid.New()
	rateID := uuid.New()
	closingBalance := int64(1_000_000)
	rateBps := 500
	recognized := int64(136)

	period := model.InterestPeriod{
		ID: periodID, Status: model.InterestPeriodOpen, ExpectedItemCount: 1,
		TotalAccruedAmount: recognized, CloseNotBeforeAt: time.Now().Add(-time.Hour).UTC(),
	}
	accrual := model.InterestDailyAccrual{
		ID: uuid.New(), PeriodID: periodID, EnrollmentID: enrollmentID,
		Status: model.InterestAccrualCompletedPosted, SnapshotID: &snapshotID,
		ClosingBalance: &closingBalance, RateVersionID: &rateID, AnnualRateBps: &rateBps,
		Denominator: denominatorString(), OpeningCarryNumerator: "0",
		ClosingCarryNumerator: "1520000", RecognizedAmount: &recognized,
		LedgerTransactionID: func() *uuid.UUID { id := uuid.New(); return &id }(),
	}

	repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(period, nil).Times(1)
	repo.EXPECT().RefreshExpectedItemCount(gomock.Any(), periodID).Return(nil).Times(1)
	repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(period, nil).Times(1)
	repo.EXPECT().ListPeriodAccruals(gomock.Any(), periodID).Return([]model.InterestDailyAccrual{accrual}, nil).Times(1)
	repo.EXPECT().CountEligibleEnrollments(gomock.Any(), periodID).Return(1, nil).Times(1)
	repo.EXPECT().HasNonActiveCapitalizationAccount(gomock.Any(), periodID).Return(false, nil).Times(1)
	repo.EXPECT().PutPeriodCheck(gomock.Any(), gomock.Any()).Return(nil).Times(3)
	repo.EXPECT().IsPreviousPeriodClosed(gomock.Any(), periodID).Return(true, nil).Times(1)

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	preview, err := svc.PreviewPeriodClose(context.Background(), periodID)
	require.NoError(t, err)
	assert.True(t, preview.Ready, "reason: %s", preview.Reason)
	assert.Equal(t, recognized, preview.ExpectedCapitalization)
	assert.Equal(t, 0, preview.BlockedItems)
	assert.Equal(t, 0, preview.MissingItems)
}

func TestPreviewPeriodClose_NotReady_BlockedAccrual(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	periodID := uuid.New()
	period := model.InterestPeriod{
		ID: periodID, Status: model.InterestPeriodOpen, ExpectedItemCount: 1,
		CloseNotBeforeAt: time.Now().Add(-time.Hour).UTC(),
	}
	blocked := model.InterestDailyAccrual{
		ID: uuid.New(), PeriodID: periodID, EnrollmentID: uuid.New(),
		Status: model.InterestAccrualBlocked,
	}

	repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(period, nil).Times(2)
	repo.EXPECT().RefreshExpectedItemCount(gomock.Any(), periodID).Return(nil).Times(1)
	repo.EXPECT().ListPeriodAccruals(gomock.Any(), periodID).Return([]model.InterestDailyAccrual{blocked}, nil).Times(1)
	repo.EXPECT().CountEligibleEnrollments(gomock.Any(), periodID).Return(1, nil).Times(1)
	repo.EXPECT().HasNonActiveCapitalizationAccount(gomock.Any(), periodID).Return(false, nil).Times(1)
	repo.EXPECT().PutPeriodCheck(gomock.Any(), gomock.Any()).Return(nil).Times(3)
	repo.EXPECT().IsPreviousPeriodClosed(gomock.Any(), periodID).Return(true, nil).Times(1)

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	preview, err := svc.PreviewPeriodClose(context.Background(), periodID)
	require.NoError(t, err)
	assert.False(t, preview.Ready)
	assert.Equal(t, 1, preview.BlockedItems)
	assert.Equal(t, "daily accrual inventory is incomplete", preview.Reason)
}

func TestPreviewPeriodClose_NotReady_NonTerminalAccrualCountsAsMissing(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	periodID := uuid.New()
	period := model.InterestPeriod{
		ID: periodID, Status: model.InterestPeriodOpen, ExpectedItemCount: 1,
		CloseNotBeforeAt: time.Now().Add(-time.Hour).UTC(),
	}
	pending := model.InterestDailyAccrual{
		ID: uuid.New(), PeriodID: periodID, EnrollmentID: uuid.New(),
		Status: model.InterestAccrualPending,
	}

	repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(period, nil).Times(2)
	repo.EXPECT().RefreshExpectedItemCount(gomock.Any(), periodID).Return(nil).Times(1)
	repo.EXPECT().ListPeriodAccruals(gomock.Any(), periodID).Return([]model.InterestDailyAccrual{pending}, nil).Times(1)
	repo.EXPECT().CountEligibleEnrollments(gomock.Any(), periodID).Return(1, nil).Times(1)
	repo.EXPECT().HasNonActiveCapitalizationAccount(gomock.Any(), periodID).Return(false, nil).Times(1)
	repo.EXPECT().PutPeriodCheck(gomock.Any(), gomock.Any()).Return(nil).Times(3)
	repo.EXPECT().IsPreviousPeriodClosed(gomock.Any(), periodID).Return(true, nil).Times(1)

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	preview, err := svc.PreviewPeriodClose(context.Background(), periodID)
	require.NoError(t, err)
	assert.False(t, preview.Ready)
	assert.Equal(t, 1, preview.MissingItems, "a non-terminal accrual must never be silently treated as complete")
}

func TestPreviewPeriodClose_NotReady_PreviousPeriodOpen(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	periodID := uuid.New()
	period := model.InterestPeriod{
		ID: periodID, Status: model.InterestPeriodOpen, ExpectedItemCount: 0,
		CloseNotBeforeAt: time.Now().Add(-time.Hour).UTC(),
	}

	repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(period, nil).Times(2)
	repo.EXPECT().RefreshExpectedItemCount(gomock.Any(), periodID).Return(nil).Times(1)
	repo.EXPECT().ListPeriodAccruals(gomock.Any(), periodID).Return(nil, nil).Times(1)
	repo.EXPECT().CountEligibleEnrollments(gomock.Any(), periodID).Return(0, nil).Times(1)
	repo.EXPECT().HasNonActiveCapitalizationAccount(gomock.Any(), periodID).Return(false, nil).Times(1)
	repo.EXPECT().PutPeriodCheck(gomock.Any(), gomock.Any()).Return(nil).Times(3)
	repo.EXPECT().IsPreviousPeriodClosed(gomock.Any(), periodID).Return(false, nil).Times(1)

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	preview, err := svc.PreviewPeriodClose(context.Background(), periodID)
	require.NoError(t, err)
	assert.False(t, preview.Ready)
	assert.Equal(t, "previous period is not closed", preview.Reason)
}

// ─── ClosePeriod ────────────────────────────────────────────────────────────

func TestClosePeriod_AlreadyClosed_ReturnsImmutableError(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	periodID := uuid.New()
	repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(model.InterestPeriod{
		ID: periodID, Status: model.InterestPeriodClosed,
	}, nil).Times(1)

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	err := svc.ClosePeriod(context.Background(), periodID, "operator")
	assert.ErrorIs(t, err, ErrClosedPeriodImmutable)
}

func TestClosePeriod_MissingActor_Rejected(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	err := svc.ClosePeriod(context.Background(), uuid.New(), "")
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestClosePeriod_NotReady_WrapsPeriodNotReady(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	periodID := uuid.New()
	period := model.InterestPeriod{
		ID: periodID, Status: model.InterestPeriodOpen, ExpectedItemCount: 1,
		CloseNotBeforeAt: time.Now().Add(-time.Hour).UTC(),
	}
	blocked := model.InterestDailyAccrual{
		ID: uuid.New(), PeriodID: periodID, EnrollmentID: uuid.New(),
		Status: model.InterestAccrualBlocked,
	}

	// current-status check + PreviewPeriodClose's two internal reads.
	repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(period, nil).Times(3)
	repo.EXPECT().RefreshExpectedItemCount(gomock.Any(), periodID).Return(nil).Times(1)
	repo.EXPECT().ListPeriodAccruals(gomock.Any(), periodID).Return([]model.InterestDailyAccrual{blocked}, nil).Times(1)
	repo.EXPECT().CountEligibleEnrollments(gomock.Any(), periodID).Return(1, nil).Times(1)
	repo.EXPECT().HasNonActiveCapitalizationAccount(gomock.Any(), periodID).Return(false, nil).Times(1)
	repo.EXPECT().PutPeriodCheck(gomock.Any(), gomock.Any()).Return(nil).Times(3)
	repo.EXPECT().IsPreviousPeriodClosed(gomock.Any(), periodID).Return(true, nil).Times(1)

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	err := svc.ClosePeriod(context.Background(), periodID, "operator")
	assert.ErrorIs(t, err, ErrPeriodNotReady)
}

func TestClosePeriod_HappyPath_PostsCapitalizationOncePerAccountAndCloses(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	periodID := uuid.New()
	enrollmentID := uuid.New()
	accountID := uuid.New()
	itemID := uuid.New()
	snapshotID := uuid.New()
	rateID := uuid.New()
	closingBalance := int64(1_000_000)
	rateBps := 500
	recognized := int64(100)
	ledgerTxID := uuid.New()

	openPeriod := model.InterestPeriod{
		ID: periodID, Status: model.InterestPeriodOpen, ExpectedItemCount: 1,
		TotalAccruedAmount: recognized, CloseNotBeforeAt: time.Now().Add(-time.Hour).UTC(),
	}
	closedTotalsPeriod := openPeriod
	closedTotalsPeriod.TotalCapitalizedAmount = recognized

	postedAccrual := model.InterestDailyAccrual{
		ID: uuid.New(), PeriodID: periodID, EnrollmentID: enrollmentID,
		Status: model.InterestAccrualCompletedPosted, SnapshotID: &snapshotID,
		ClosingBalance: &closingBalance, RateVersionID: &rateID, AnnualRateBps: &rateBps,
		Denominator: denominatorString(), OpeningCarryNumerator: "0",
		ClosingCarryNumerator: "0", RecognizedAmount: &recognized,
		LedgerTransactionID: &ledgerTxID,
	}

	pendingItem := model.InterestCapitalizationItem{
		ID: itemID, PeriodID: periodID, EnrollmentID: enrollmentID, AccountID: accountID,
		CapitalizationAmount: recognized, Status: model.InterestCapitalizationPending,
	}
	claimedItem := pendingItem
	claimedItem.Status = model.InterestCapitalizationProcessing
	postedItem := pendingItem
	postedItem.Status = model.InterestCapitalizationPosted
	postedItem.LedgerTransactionID = &ledgerTxID

	// GetPeriod call sequence: (1) current-status check, (2)+(3) inside
	// PreviewPeriodClose, (4) re-read after preview, (5) final reconciliation.
	gomock.InOrder(
		repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(openPeriod, nil),
		repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(openPeriod, nil),
		repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(openPeriod, nil),
		repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(openPeriod, nil),
		repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(closedTotalsPeriod, nil),
	)
	repo.EXPECT().RefreshExpectedItemCount(gomock.Any(), periodID).Return(nil).Times(1)
	repo.EXPECT().ListPeriodAccruals(gomock.Any(), periodID).Return([]model.InterestDailyAccrual{postedAccrual}, nil).Times(1)
	repo.EXPECT().CountEligibleEnrollments(gomock.Any(), periodID).Return(1, nil).Times(1)
	repo.EXPECT().HasNonActiveCapitalizationAccount(gomock.Any(), periodID).Return(false, nil).Times(1)
	repo.EXPECT().PutPeriodCheck(gomock.Any(), gomock.Any()).Return(nil).Times(4)
	repo.EXPECT().IsPreviousPeriodClosed(gomock.Any(), periodID).Return(true, nil).Times(1)

	repo.EXPECT().MarkPeriodStatus(gomock.Any(), gomock.Any(), periodID, model.InterestPeriodClosing, "").Return(nil).Times(1)
	repo.EXPECT().EnsureCapitalizationItems(gomock.Any(), periodID).Return(nil).Times(1)
	repo.EXPECT().ListCapitalizationItems(gomock.Any(), periodID).Return([]model.InterestCapitalizationItem{pendingItem}, nil).Times(1)
	repo.EXPECT().StartCapitalization(gomock.Any(), itemID, gomock.Any(), gomock.Any()).Return(claimedItem, nil).Times(1)
	repo.EXPECT().CompleteCapitalization(gomock.Any(), gomock.Any(), itemID, model.InterestCapitalizationPosted, gomock.Any()).Return(nil).Times(1)
	repo.EXPECT().ListCapitalizationItems(gomock.Any(), periodID).Return([]model.InterestCapitalizationItem{postedItem}, nil).Times(1)
	repo.EXPECT().MarkPeriodStatus(gomock.Any(), gomock.Any(), periodID, model.InterestPeriodClosed, "").Return(nil).Times(1)

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	err := svc.ClosePeriod(context.Background(), periodID, "operator")
	require.NoError(t, err)
}

// ─── CreateAdjustment ───────────────────────────────────────────────────────

func TestCreateAdjustment_MissingPeriodOrEnrollment_Rejected(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	_, err := svc.CreateAdjustment(context.Background(), model.InterestAdjustment{})
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreateAdjustment_InvalidDirection_Rejected(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	accrualID := uuid.New()
	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	_, err := svc.CreateAdjustment(context.Background(), model.InterestAdjustment{
		SourcePeriodID: uuid.New(), EnrollmentID: uuid.New(), Direction: "sideways",
		SourceAccrualID: &accrualID,
	})
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreateAdjustment_NeitherSourceLinked_Rejected(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	_, err := svc.CreateAdjustment(context.Background(), model.InterestAdjustment{
		SourcePeriodID: uuid.New(), EnrollmentID: uuid.New(), Direction: "positive",
	})
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreateAdjustment_BothSourcesLinked_Rejected(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	accrualID := uuid.New()
	capID := uuid.New()
	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	_, err := svc.CreateAdjustment(context.Background(), model.InterestAdjustment{
		SourcePeriodID: uuid.New(), EnrollmentID: uuid.New(), Direction: "positive",
		SourceAccrualID: &accrualID, SourceCapitalizationID: &capID,
	})
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreateAdjustment_SourcePeriodNotClosed_Rejected(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	periodID := uuid.New()
	accrualID := uuid.New()
	repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(model.InterestPeriod{
		ID: periodID, Status: model.InterestPeriodOpen,
	}, nil).Times(1)

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	_, err := svc.CreateAdjustment(context.Background(), model.InterestAdjustment{
		SourcePeriodID: periodID, EnrollmentID: uuid.New(), Direction: "positive",
		SourceAccrualID: &accrualID,
	})
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreateAdjustment_ClosedSourcePeriod_Accepted(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	periodID := uuid.New()
	accrualID := uuid.New()
	input := model.InterestAdjustment{
		SourcePeriodID: periodID, EnrollmentID: uuid.New(), Direction: "positive",
		SourceAccrualID: &accrualID, Amount: 10, Reason: "manual correction",
	}
	repo.EXPECT().GetPeriod(gomock.Any(), periodID).Return(model.InterestPeriod{
		ID: periodID, Status: model.InterestPeriodClosed,
	}, nil).Times(1)
	repo.EXPECT().CreateAdjustment(gomock.Any(), input).Return(input, nil).Times(1)

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	got, err := svc.CreateAdjustment(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, input.Amount, got.Amount)
}

// ─── ApproveAdjustment ──────────────────────────────────────────────────────

func TestApproveAdjustment_MissingChecker_Rejected(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	err := svc.ApproveAdjustment(context.Background(), uuid.New(), "")
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestApproveAdjustment_AlreadyPosted_IsIdempotentNoOp(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	id := uuid.New()
	repo.EXPECT().GetAdjustment(gomock.Any(), id).Return(model.InterestAdjustment{
		ID: id, Status: "posted",
	}, nil).Times(1)

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	err := svc.ApproveAdjustment(context.Background(), id, "checker")
	assert.NoError(t, err)
}

func TestApproveAdjustment_PendingApproval_ApprovesPostsAndMarks(t *testing.T) {
	repo, ctrl := newMockInterestRepo(t)
	defer ctrl.Finish()

	id := uuid.New()
	enrollmentID := uuid.New()
	productID := uuid.New()
	periodID := uuid.New()
	accrualID := uuid.New()

	pending := model.InterestAdjustment{
		ID: id, Status: "pending_approval", EnrollmentID: enrollmentID,
		SourcePeriodID: periodID, Amount: 50, Direction: "positive",
		SourceAccrualID: &accrualID,
	}
	approved := pending
	approved.Status = "approved"

	repo.EXPECT().GetAdjustment(gomock.Any(), id).Return(pending, nil).Times(1)
	repo.EXPECT().ApproveAdjustment(gomock.Any(), id, "checker").Return(nil).Times(1)
	repo.EXPECT().GetAdjustment(gomock.Any(), id).Return(approved, nil).Times(1)
	repo.EXPECT().GetEnrollment(gomock.Any(), enrollmentID).Return(model.SavingsEnrollment{
		ID: enrollmentID, ProductID: productID, AccountID: uuid.New(), UserID: uuid.New(),
	}, nil).Times(1)
	repo.EXPECT().GetProduct(gomock.Any(), productID).Return(model.SavingsProduct{
		ID: productID, Currency: "IDR",
	}, nil).Times(1)
	repo.EXPECT().MarkAdjustmentPosted(gomock.Any(), id, gomock.Any()).Return(nil).Times(1)

	svc := New(mockDB{}, repo, nil, &fakePoster{}, nil, nil)
	err := svc.ApproveAdjustment(context.Background(), id, "checker")
	require.NoError(t, err)
}
