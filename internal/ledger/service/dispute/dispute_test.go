package dispute

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	repository_mock "github.com/herdifirdausss/seev/internal/ledger/repository"
)

func newMockDisputeRepo(t *testing.T) (*repository_mock.MockChargebackDisputeRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockChargebackDisputeRepository(ctrl), ctrl
}

// fakeTxReader is a hand-written test double for OriginalTxReader.
type fakeTxReader struct {
	tx  model.LedgerTransaction
	err error
}

func (f *fakeTxReader) GetByID(context.Context, uuid.UUID) (model.LedgerTransaction, error) {
	return f.tx, f.err
}

// ─── OpenDispute: validation ────────────────────────────────────────────────

func TestOpenDispute_EmptyDisputeRef_Rejected(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	s := New(repo, &fakeTxReader{})
	_, err := s.OpenDispute(context.Background(), uuid.New(), "", "visa", "", decimal.NewFromInt(100), "IDR", nil, "ops-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestOpenDispute_UnknownCardNetwork_Rejected(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	s := New(repo, &fakeTxReader{})
	_, err := s.OpenDispute(context.Background(), uuid.New(), "dp-1", "discover", "", decimal.NewFromInt(100), "IDR", nil, "ops-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestOpenDispute_NonPositiveAmount_Rejected(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	s := New(repo, &fakeTxReader{})
	_, err := s.OpenDispute(context.Background(), uuid.New(), "dp-1", "visa", "", decimal.Zero, "IDR", nil, "ops-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestOpenDispute_EmptyCreatedBy_Rejected(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	s := New(repo, &fakeTxReader{})
	_, err := s.OpenDispute(context.Background(), uuid.New(), "dp-1", "visa", "", decimal.NewFromInt(100), "IDR", nil, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestOpenDispute_OriginalNotFound_Rejected(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	s := New(repo, &fakeTxReader{err: apperror.ErrTransactionNotFound})
	_, err := s.OpenDispute(context.Background(), uuid.New(), "dp-1", "visa", "", decimal.NewFromInt(100), "IDR", nil, "ops-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrOriginalNotFound)
}

func TestOpenDispute_OriginalNotPosted_Rejected(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	s := New(repo, &fakeTxReader{tx: model.LedgerTransaction{Status: "failed", Currency: "IDR"}})
	_, err := s.OpenDispute(context.Background(), uuid.New(), "dp-1", "visa", "", decimal.NewFromInt(100), "IDR", nil, "ops-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrNotReversible)
}

func TestOpenDispute_CurrencyMismatch_Rejected(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	s := New(repo, &fakeTxReader{tx: model.LedgerTransaction{Status: "posted", Currency: "IDR"}})
	_, err := s.OpenDispute(context.Background(), uuid.New(), "dp-1", "visa", "", decimal.NewFromInt(100), "USD", nil, "ops-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrCurrencyMismatch)
}

func TestOpenDispute_ValidCase_CreatesWithOriginalCurrency(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	originalTxID := uuid.New()
	s := New(repo, &fakeTxReader{tx: model.LedgerTransaction{Status: "posted", Currency: "IDR"}})

	var created model.ChargebackDispute
	repo.EXPECT().CreateDispute(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, d model.ChargebackDispute) error {
		created = d
		return nil
	})

	id, err := s.OpenDispute(context.Background(), originalTxID, "dp-1", "visa", "10.4", decimal.NewFromInt(50_000), "", nil, "ops-1")
	require.NoError(t, err)
	assert.Equal(t, id, created.ID)
	assert.Equal(t, originalTxID, created.OriginalTxID)
	assert.Equal(t, "IDR", created.Currency, "empty currency must default to the original transaction's currency")
	assert.Equal(t, "visa", created.CardNetwork)
	assert.Equal(t, "10.4", created.ReasonCode)
}

// ─── SubmitEvidence / ResolveDispute / LinkChargebackTx ─────────────────────

func TestSubmitEvidence_EmptyRef_Rejected(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	s := New(repo, &fakeTxReader{})
	err := s.SubmitEvidence(context.Background(), uuid.New(), "", "ops-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestSubmitEvidence_EmptyChangedBy_Rejected(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	s := New(repo, &fakeTxReader{})
	err := s.SubmitEvidence(context.Background(), uuid.New(), "evidence-1", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestSubmitEvidence_ZeroRows_DiagnosesAlreadyResolved(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	id := uuid.New()
	repo.EXPECT().SubmitEvidence(gomock.Any(), id, "evidence-1", "ops-1").Return(int64(0), nil)
	repo.EXPECT().GetDispute(gomock.Any(), id).Return(model.ChargebackDispute{ID: id, Status: "won"}, nil)

	s := New(repo, &fakeTxReader{})
	err := s.SubmitEvidence(context.Background(), id, "evidence-1", "ops-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrChargebackDisputeAlreadyResolved)
}

func TestResolveDispute_InvalidStatus_Rejected(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	s := New(repo, &fakeTxReader{})
	err := s.ResolveDispute(context.Background(), uuid.New(), "open", "ops-1", "reason")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestResolveDispute_EmptyResolvedBy_Rejected(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	s := New(repo, &fakeTxReader{})
	err := s.ResolveDispute(context.Background(), uuid.New(), "won", "", "reason")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestResolveDispute_EmptyReason_Rejected(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	s := New(repo, &fakeTxReader{})
	err := s.ResolveDispute(context.Background(), uuid.New(), "won", "ops-1", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestResolveDispute_Success(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	id := uuid.New()
	repo.EXPECT().ResolveDispute(gomock.Any(), id, "lost", "ops-1", "network ruled against us").Return(int64(1), nil)

	s := New(repo, &fakeTxReader{})
	require.NoError(t, s.ResolveDispute(context.Background(), id, "lost", "ops-1", "network ruled against us"))
}

func TestLinkChargebackTx_AlreadyLinked_Rejected(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	id, chargebackTxID := uuid.New(), uuid.New()
	repo.EXPECT().LinkChargebackTx(gomock.Any(), id, chargebackTxID).Return(int64(0), nil)
	repo.EXPECT().GetDispute(gomock.Any(), id).Return(model.ChargebackDispute{ID: id, Status: "open"}, nil)

	s := New(repo, &fakeTxReader{})
	err := s.LinkChargebackTx(context.Background(), id, chargebackTxID)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrChargebackDisputeAlreadyResolved)
}

func TestListStatusChanges_DelegatesToRepo(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	id := uuid.New()
	want := []model.ChargebackDisputeStatusChange{{DisputeID: id, FromStatus: "open", ToStatus: "evidence_submitted", ChangedBy: "ops-1"}}
	repo.EXPECT().ListStatusChanges(gomock.Any(), id).Return(want, nil)

	s := New(repo, &fakeTxReader{})
	got, err := s.ListStatusChanges(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestListOpenDisputes_ClampsLimit(t *testing.T) {
	repo, ctrl := newMockDisputeRepo(t)
	defer ctrl.Finish()
	repo.EXPECT().ListOpenDisputes(gomock.Any(), defaultListLimit, 0).Return(nil, nil)

	s := New(repo, &fakeTxReader{})
	_, err := s.ListOpenDisputes(context.Background(), 0, -5)
	require.NoError(t, err)
}
