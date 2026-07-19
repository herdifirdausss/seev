package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	repository_mock "github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// =============================================================================
// Test Helpers
// =============================================================================

func d(s string) decimal.Decimal {
	v, _ := decimal.NewFromString(s)
	return v
}

func newID() uuid.UUID { return uuid.New() }

func newMockAccountBalanceRepo(t *testing.T) (*repository_mock.MockBalanceRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockBalanceRepository(ctrl), ctrl
}

func newMockTransactionRepo(t *testing.T) (*repository_mock.MockTransactionRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockTransactionRepository(ctrl), ctrl
}

func newMockEntryRepo(t *testing.T) (*repository_mock.MockEntryRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockEntryRepository(ctrl), ctrl
}

func newMockOutboxRepo(t *testing.T) (*repository_mock.MockOutboxRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockOutboxRepository(ctrl), ctrl
}

func newMockTxProcessor(t *testing.T) (*processors.MockTxProcessor, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return processors.NewMockTxProcessor(ctrl), ctrl
}

// =============================================================================
// Mock Infrastructure
// =============================================================================

type mockDB struct{}

func (m *mockDB) WithTx(_ context.Context, _ *sql.TxOptions, fn func(*sql.Tx) error) error {
	return fn(nil)
}

// =============================================================================
// ProcessorRegistry Tests
// =============================================================================

func TestRegistry_Get_Known(t *testing.T) {
	p, ctrl := newMockTxProcessor(t)
	defer ctrl.Finish()
	p.EXPECT().Type().Return("money_in").AnyTimes()
	reg := processors.NewRegistry(p)
	got, err := reg.Get("money_in")
	require.NoError(t, err)
	assert.Equal(t, p, got)
}

func TestRegistry_Get_Unknown_WrapsErrUnknownProcessor(t *testing.T) {
	reg := processors.NewRegistry()
	_, err := reg.Get("nope")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrUnknownProcessor)
}

func TestRegistry_DuplicateType_Panics(t *testing.T) {
	p, ctrl := newMockTxProcessor(t)
	defer ctrl.Finish()
	p.EXPECT().Type().Return("x").AnyTimes()
	assert.Panics(t, func() {
		processors.NewRegistry(p, p)
	})
}

// =============================================================================
// Handle — guard tests
// =============================================================================

func TestHandle_EmptyKey_Rejected(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()

	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(), nil, decimal.Zero, nil)
	err := svc.Handle(context.Background(), processors.Command{IdempotencyKey: "", Type: "money_in", Amount: d("100")})
	assert.ErrorIs(t, err, apperror.ErrEmptyIdempotencyKey)
}

func TestHandle_WhitespaceKey_Rejected(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()

	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(), nil, decimal.Zero, nil)
	err := svc.Handle(context.Background(), processors.Command{IdempotencyKey: "   ", Type: "money_in", Amount: d("100")})
	assert.ErrorIs(t, err, apperror.ErrEmptyIdempotencyKey)
}

func TestHandle_FractionalAmount_Rejected(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()

	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(), nil, decimal.Zero, nil)
	err := svc.Handle(context.Background(), processors.Command{IdempotencyKey: "k1", Type: "money_in", Amount: d("100.50")})
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

// ─── Handle — amount cap (docs/plan/10 Task T5) ────────────────────────────────

func TestHandle_AmountExceedsCap_Rejected_NoDBWork(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()

	// No expectations set on any mock repo — the cap check must fire
	// before any DB work, same as the integer-amount check above it.
	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(), nil, decimal.NewFromInt(1000), nil)
	err := svc.Handle(context.Background(), processors.Command{IdempotencyKey: "k1", Type: "money_in", Amount: d("1001")})
	assert.ErrorIs(t, err, apperror.ErrAmountTooLarge)
}

func TestHandle_AmountAtCap_Allowed(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()

	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(), nil, decimal.NewFromInt(1000), nil)
	// Exactly at the cap — must pass the cap check itself and fail later
	// for an unrelated reason (unknown processor type), proving the cap
	// comparison is GreaterThan, not GreaterThanOrEqual.
	err := svc.Handle(context.Background(), processors.Command{IdempotencyKey: "k1", Type: "not_a_real_type", Amount: d("1000")})
	assert.NotErrorIs(t, err, apperror.ErrAmountTooLarge)
}

func TestHandle_NoCapConfigured_UnboundedAmountAllowed(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()

	// decimal.Zero means "no cap configured" — even a huge amount must not
	// be rejected by the cap check (it may still fail for other reasons).
	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(), nil, decimal.Zero, nil)
	err := svc.Handle(context.Background(), processors.Command{IdempotencyKey: "k1", Type: "not_a_real_type", Amount: d("999999999999")})
	assert.NotErrorIs(t, err, apperror.ErrAmountTooLarge)
}

func TestHandle_UnknownType_Rejected(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()
	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(), nil, decimal.Zero, nil)
	err := svc.Handle(context.Background(), processors.Command{IdempotencyKey: "k1", Type: "unknown", Amount: d("1")})
	assert.ErrorIs(t, err, apperror.ErrUnknownProcessor)
}

func TestHandle_ResolveError_Propagated(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()
	p, ctrl := newMockTxProcessor(t)
	defer ctrl.Finish()
	p.EXPECT().Type().Return("t").AnyTimes()
	// ctx carries span data from tracer.Start, so it's no longer == context.Background().
	p.EXPECT().ValidateCommand(gomock.Any(), processors.Command{IdempotencyKey: "k", Type: "t", Amount: d("1")}).Return(nil)
	p.EXPECT().ResolveAccounts(gomock.Any(), processors.Command{IdempotencyKey: "k", Type: "t", Amount: d("1")}).Return(processors.ResolvedAccounts{}, "IDR", errors.New("db down"))
	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(p), nil, decimal.Zero, nil)
	err := svc.Handle(context.Background(), processors.Command{IdempotencyKey: "k", Type: "t", Amount: d("1")})
	assert.ErrorContains(t, err, "db down")
}

func TestHandle_ValidateCommandError_Propagated(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()
	p, ctrl := newMockTxProcessor(t)
	defer ctrl.Finish()
	p.EXPECT().Type().Return("t").AnyTimes()
	// ctx carries span data from tracer.Start, so it's no longer == context.Background().
	p.EXPECT().ValidateCommand(gomock.Any(), processors.Command{IdempotencyKey: "k", Type: "t", Amount: d("1")}).Return(errors.New("missing required metadata"))
	// ResolveAccounts must NOT be called when ValidateCommand rejects the command upfront.
	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(p), nil, decimal.Zero, nil)
	err := svc.Handle(context.Background(), processors.Command{IdempotencyKey: "k", Type: "t", Amount: d("1")})
	assert.ErrorContains(t, err, "missing required metadata")
}

// ─── Handle — status label: business rejection vs infra error (docs/plan/43 T5) ─

// TestHandle_BusinessRejection_RecordsStatusRejected is the regression test
// for the posting_availability SLI gap found while writing T5: a valid
// business/input rejection (here, an empty idempotency key) must record
// ledger_transactions_total{status="rejected"}, NOT status="error" — an
// "error" observation is what the SLO recording rule counts as a bad event,
// and a legitimate rejection is not an outage (see
// apperror.IsBusinessRejection's doc comment).
func TestHandle_BusinessRejection_RecordsStatusRejected(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()

	before := testutil.ToFloat64(transactionsTotal.WithLabelValues("money_in", "rejected"))

	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(), nil, decimal.Zero, nil)
	err := svc.Handle(context.Background(), processors.Command{IdempotencyKey: "", Type: "money_in", Amount: d("100")})
	require.ErrorIs(t, err, apperror.ErrEmptyIdempotencyKey)

	assert.Equal(t, before+1, testutil.ToFloat64(transactionsTotal.WithLabelValues("money_in", "rejected")))
}

// TestHandle_InfraError_RecordsStatusError is the counterpart: an
// unrecognized error (not one of apperror's known business sentinels, e.g. a
// raw "db down" propagated from a repository call) must still record
// status="error" — the SLO's bad-event bucket must not silently shrink to
// zero.
func TestHandle_InfraError_RecordsStatusError(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()
	p, ctrl := newMockTxProcessor(t)
	defer ctrl.Finish()
	p.EXPECT().Type().Return("t").AnyTimes()
	p.EXPECT().ValidateCommand(gomock.Any(), processors.Command{IdempotencyKey: "k", Type: "t", Amount: d("1")}).Return(nil)
	p.EXPECT().ResolveAccounts(gomock.Any(), processors.Command{IdempotencyKey: "k", Type: "t", Amount: d("1")}).Return(processors.ResolvedAccounts{}, "IDR", errors.New("db down"))

	before := testutil.ToFloat64(transactionsTotal.WithLabelValues("t", "error"))

	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(p), nil, decimal.Zero, nil)
	err := svc.Handle(context.Background(), processors.Command{IdempotencyKey: "k", Type: "t", Amount: d("1")})
	require.ErrorContains(t, err, "db down")

	assert.Equal(t, before+1, testutil.ToFloat64(transactionsTotal.WithLabelValues("t", "error")))
}

func TestHandle_SourceNotInOrdered_RejectedAsProcessorBug(t *testing.T) {
	// docs/plan/14 Task T1: a processor claiming a Source/Destination that
	// isn't even in its own Ordered account list is a processor bug, not a
	// user-facing error — Handle must refuse to post rather than write a
	// wrong/unrelated account into ledger_transactions.source_account_id.
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()
	p, ctrl := newMockTxProcessor(t)
	defer ctrl.Finish()

	inOrdered := uuid.New()
	notOrdered := uuid.New() // deliberately absent from Ordered below

	p.EXPECT().Type().Return("t").AnyTimes()
	p.EXPECT().ValidateCommand(gomock.Any(), processors.Command{IdempotencyKey: "k", Type: "t", Amount: d("1")}).Return(nil)
	p.EXPECT().ResolveAccounts(gomock.Any(), processors.Command{IdempotencyKey: "k", Type: "t", Amount: d("1")}).Return(
		processors.ResolvedAccounts{Ordered: []uuid.UUID{inOrdered}, Source: notOrdered, Destination: inOrdered}, "IDR", nil)

	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(p), nil, decimal.Zero, nil)
	err := svc.Handle(context.Background(), processors.Command{IdempotencyKey: "k", Type: "t", Amount: d("1")})

	require.Error(t, err)
	assert.ErrorContains(t, err, "not in Ordered")
}

// =============================================================================
// Validators
// =============================================================================

func TestPositiveAmount_Zero(t *testing.T) {
	v := processors.PositiveAmountValidator{}
	cmd := processors.ResolvedCommand{Command: processors.Command{Amount: d("0")}}
	err := v.Validate(context.Background(), nil, cmd, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrAmountTooSmall)
}

func TestPositiveAmount_Negative(t *testing.T) {
	v := processors.PositiveAmountValidator{}
	cmd := processors.ResolvedCommand{Command: processors.Command{Amount: d("-1")}}
	err := v.Validate(context.Background(), nil, cmd, nil)
	assert.ErrorIs(t, err, apperror.ErrAmountTooSmall)
}

func TestPositiveAmount_Positive_OK(t *testing.T) {
	v := processors.PositiveAmountValidator{}
	cmd := processors.ResolvedCommand{Command: processors.Command{Amount: d("0.01")}}
	assert.NoError(t, v.Validate(context.Background(), nil, cmd, nil))
}

func TestMaxAmount_Above_Rejected(t *testing.T) {
	v := processors.MaxAmountValidator{Max: d("100")}
	cmd := processors.ResolvedCommand{Command: processors.Command{Amount: d("100.01")}}
	err := v.Validate(context.Background(), nil, cmd, nil)
	assert.ErrorIs(t, err, apperror.ErrAmountTooLarge)
}

func TestMaxAmount_AtMax_OK(t *testing.T) {
	v := processors.MaxAmountValidator{Max: d("100")}
	cmd := processors.ResolvedCommand{Command: processors.Command{Amount: d("100")}}
	assert.NoError(t, v.Validate(context.Background(), nil, cmd, nil))
}

func TestSufficientFunds_Sufficient_OK(t *testing.T) {
	id := newID()
	v := processors.SufficientFundsValidator{AccountID: id}
	cmd := processors.ResolvedCommand{Command: processors.Command{Amount: d("100")}}
	bal := map[uuid.UUID]model.AccountBalance{id: {Balance: d("200")}}
	assert.NoError(t, v.Validate(context.Background(), nil, cmd, bal))
}

func TestSufficientFunds_Exact_OK(t *testing.T) {
	id := newID()
	v := processors.SufficientFundsValidator{AccountID: id}
	cmd := processors.ResolvedCommand{Command: processors.Command{Amount: d("99.99")}}
	bal := map[uuid.UUID]model.AccountBalance{id: {Balance: d("99.99")}}
	assert.NoError(t, v.Validate(context.Background(), nil, cmd, bal))
}

func TestSufficientFunds_Insufficient_ErrInsufficientFunds(t *testing.T) {
	id := newID()
	v := processors.SufficientFundsValidator{AccountID: id}
	cmd := processors.ResolvedCommand{Command: processors.Command{Amount: d("500")}}
	bal := map[uuid.UUID]model.AccountBalance{id: {Balance: d("100")}}
	err := v.Validate(context.Background(), nil, cmd, bal)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrInsufficientFunds)
}

func TestNotSelfTransfer_Different_OK(t *testing.T) {
	v := processors.NotSelfTransferValidator{A: newID(), B: newID()}
	assert.NoError(t, v.Validate(context.Background(), nil, processors.ResolvedCommand{}, nil))
}

func TestNotSelfTransfer_Same_Rejected(t *testing.T) {
	id := newID()
	v := processors.NotSelfTransferValidator{A: id, B: id}
	err := v.Validate(context.Background(), nil, processors.ResolvedCommand{}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrSelfTransfer)
}

func TestFeeAmountValidator_Valid(t *testing.T) {
	v := processors.FeeAmountValidator{}
	cmd := processors.ResolvedCommand{Command: processors.Command{Amount: d("100"), Metadata: map[string]any{"fee_amount": "10"}}}
	assert.NoError(t, v.Validate(context.Background(), nil, cmd, nil))
}

func TestFeeAmountValidator_FeeEqualAmount_Rejected(t *testing.T) {
	v := processors.FeeAmountValidator{}
	cmd := processors.ResolvedCommand{Command: processors.Command{Amount: d("100"), Metadata: map[string]any{"fee_amount": "100"}}}
	err := v.Validate(context.Background(), nil, cmd, nil)
	assert.ErrorIs(t, err, apperror.ErrFeeExceedsAmount)
}

func TestFeeAmountValidator_FeeExceedsAmount_Rejected(t *testing.T) {
	v := processors.FeeAmountValidator{}
	cmd := processors.ResolvedCommand{Command: processors.Command{Amount: d("100"), Metadata: map[string]any{"fee_amount": "101"}}}
	err := v.Validate(context.Background(), nil, cmd, nil)
	assert.ErrorIs(t, err, apperror.ErrFeeExceedsAmount)
}

// =============================================================================
// MultiValidator
// =============================================================================

func TestMultiValidator_FailFast(t *testing.T) {
	called := false
	spy := &spyValidator{fn: func() error { called = true; return nil }}
	mv := processors.MultiValidator{processors.PositiveAmountValidator{}, spy} // first fails
	cmd := processors.ResolvedCommand{Command: processors.Command{Amount: d("0")}}
	mv.Validate(context.Background(), nil, cmd, nil) //nolint
	assert.False(t, called, "second validator must not run after first fails")
}

func TestMultiValidator_AllPass(t *testing.T) {
	mv := processors.MultiValidator{processors.PositiveAmountValidator{}, processors.MinAmountValidator{Min: d("10")}}
	cmd := processors.ResolvedCommand{Command: processors.Command{Amount: d("50")}}
	assert.NoError(t, mv.Validate(context.Background(), nil, cmd, nil))
}

type spyValidator struct{ fn func() error }

func (s *spyValidator) Validate(_ context.Context, _ *sql.Tx, _ processors.ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) error {
	return s.fn()
}

// =============================================================================
// validateBalanced
// =============================================================================

func TestValidateBalanced_Simple(t *testing.T) {
	a, b := newID(), newID()
	err := validateBalanced([]model.EntryInstruction{
		{AccountID: a, Direction: constant.Debit, Amount: d("100")},
		{AccountID: b, Direction: constant.Credit, Amount: d("100")},
	})
	assert.NoError(t, err)
}

func TestValidateBalanced_ThreeEntry(t *testing.T) {
	a, b, c := newID(), newID(), newID()
	err := validateBalanced([]model.EntryInstruction{
		{AccountID: a, Direction: constant.Debit, Amount: d("100")},
		{AccountID: b, Direction: constant.Credit, Amount: d("90")},
		{AccountID: c, Direction: constant.Credit, Amount: d("10")},
	})
	assert.NoError(t, err)
}

func TestValidateBalanced_Unbalanced_Error(t *testing.T) {
	a, b := newID(), newID()
	err := validateBalanced([]model.EntryInstruction{
		{AccountID: a, Direction: constant.Debit, Amount: d("100")},
		{AccountID: b, Direction: constant.Credit, Amount: d("99")},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, apperror.ErrUnbalancedEntries)
}

func TestValidateBalanced_ZeroAmount_Error(t *testing.T) {
	err := validateBalanced([]model.EntryInstruction{
		{AccountID: newID(), Direction: constant.Debit, Amount: d("0")},
	})
	require.Error(t, err)
}

// =============================================================================
// applyEntries  [FIX #4 — single-pass computation]
// =============================================================================

func TestApplyEntries_SimpleTransfer(t *testing.T) {
	from, to := newID(), newID()
	bal := map[uuid.UUID]model.AccountBalance{
		from: {Balance: d("1000")},
		to:   {Balance: d("200")},
	}
	entries := []model.EntryInstruction{
		{AccountID: from, Direction: constant.Debit, Amount: d("300")},
		{AccountID: to, Direction: constant.Credit, Amount: d("300")},
	}
	result := applyEntries(bal, entries)
	assert.True(t, result[from].Equal(d("700")))
	assert.True(t, result[to].Equal(d("500")))
}

func TestApplyEntries_ThreeEntry(t *testing.T) {
	user, merchant, fee := newID(), newID(), newID()
	bal := map[uuid.UUID]model.AccountBalance{
		user:     {Balance: d("1000")},
		merchant: {Balance: d("500")},
		fee:      {Balance: d("0")},
	}
	entries := []model.EntryInstruction{
		{AccountID: user, Direction: constant.Debit, Amount: d("100")},
		{AccountID: merchant, Direction: constant.Credit, Amount: d("95")},
		{AccountID: fee, Direction: constant.Credit, Amount: d("5")},
	}
	result := applyEntries(bal, entries)
	assert.True(t, result[user].Equal(d("900")))
	assert.True(t, result[merchant].Equal(d("595")))
	assert.True(t, result[fee].Equal(d("5")))
}

// =============================================================================
// validateAccounts
// =============================================================================

func TestValidateAccounts_Active_OK(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()
	id := newID()
	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(), nil, decimal.Zero, nil)
	cmd := processors.ResolvedCommand{AccountIDs: []uuid.UUID{id}, Currency: "IDR"}
	bal := map[uuid.UUID]model.AccountBalance{id: {Status: constant.AccountStatusActive, Currency: "IDR"}}
	assert.NoError(t, svc.validateAccounts(cmd, bal))
}

func TestValidateAccounts_Missing_ErrAccountNotFound(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()
	id := newID()
	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(), nil, decimal.Zero, nil)
	cmd := processors.ResolvedCommand{AccountIDs: []uuid.UUID{id}, Currency: "IDR"}
	err := svc.validateAccounts(cmd, map[uuid.UUID]model.AccountBalance{})
	assert.ErrorIs(t, err, apperror.ErrAccountNotFound)
}

func TestValidateAccounts_Suspended_ErrAccountSuspended(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()
	id := newID()
	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(), nil, decimal.Zero, nil)
	cmd := processors.ResolvedCommand{AccountIDs: []uuid.UUID{id}, Currency: "IDR"}
	bal := map[uuid.UUID]model.AccountBalance{id: {Status: constant.AccountStatusSuspended, Currency: "IDR"}}
	assert.ErrorIs(t, svc.validateAccounts(cmd, bal), apperror.ErrAccountSuspended)
}

func TestValidateAccounts_Closed_ErrAccountClosed(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()
	id := newID()
	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(), nil, decimal.Zero, nil)
	cmd := processors.ResolvedCommand{AccountIDs: []uuid.UUID{id}, Currency: "IDR"}
	bal := map[uuid.UUID]model.AccountBalance{id: {Status: constant.AccountStatusClosed, Currency: "IDR"}}
	assert.ErrorIs(t, svc.validateAccounts(cmd, bal), apperror.ErrAccountClosed)
}

func TestValidateAccounts_CurrencyMismatch_ErrCurrencyMismatch(t *testing.T) {
	transactionRepo, ctrl := newMockTransactionRepo(t)
	defer ctrl.Finish()
	balanceRepo, ctrl2 := newMockAccountBalanceRepo(t)
	defer ctrl2.Finish()
	entryRepo, ctrl3 := newMockEntryRepo(t)
	defer ctrl3.Finish()
	outboxRepo, ctrl4 := newMockOutboxRepo(t)
	defer ctrl4.Finish()
	id := newID()
	svc := New(&mockDB{}, transactionRepo, balanceRepo, entryRepo, outboxRepo, processors.NewRegistry(), nil, decimal.Zero, nil)
	cmd := processors.ResolvedCommand{AccountIDs: []uuid.UUID{id}, Currency: "IDR"}
	bal := map[uuid.UUID]model.AccountBalance{id: {Status: constant.AccountStatusActive, Currency: "USD"}}
	assert.ErrorIs(t, svc.validateAccounts(cmd, bal), apperror.ErrCurrencyMismatch)
}

// =============================================================================
// generalutil.MetaDecimal — float64 precision correctness  [FIX #1 iter2]
// =============================================================================

func TestMetaDecimal_StringInput_Exact(t *testing.T) {
	d, err := generalutil.MetaDecimal(map[string]any{"v": "0.10"}, "v")
	require.NoError(t, err)
	assert.Equal(t, "0.10", d.StringFixed(2))
}

func TestMetaDecimal_MissingKey_Error(t *testing.T) {
	_, err := generalutil.MetaDecimal(map[string]any{}, "v")
	require.Error(t, err)
}

func TestDecimalArithmetic_Precision(t *testing.T) {
	// Classic float64 trap: 0.1 + 0.2 != 0.3
	sum := d("0.10").Add(d("0.20"))
	assert.True(t, sum.Equal(d("0.30")), "0.10 + 0.20 must exactly equal 0.30")

	// Cumulative addition
	total := decimal.Zero
	for i := 0; i < 10; i++ {
		total = total.Add(d("0.10"))
	}
	assert.True(t, total.Equal(d("1.00")))
}

// =============================================================================
// LedgerError structured type  [FIX #13 iter3]
// =============================================================================

func TestLedgerError_ErrorsIs_Sentinel(t *testing.T) {
	err := apperror.NewBizErr(apperror.ErrInsufficientFunds, "balance too low")
	assert.ErrorIs(t, err, apperror.ErrInsufficientFunds)
}

func TestLedgerError_ErrorsAs(t *testing.T) {
	err := apperror.NewBizErr(apperror.ErrAmountTooLarge, "exceeds limit")
	var le *apperror.LedgerError
	require.True(t, errors.As(err, &le))
	assert.Equal(t, "AMOUNT_TOO_LARGE", le.Code)
	assert.False(t, le.Retryable)
}

func TestLedgerError_Wrapped_ErrorsIs(t *testing.T) {
	inner := apperror.NewBizErr(apperror.ErrDailyLimitExceeded, "over daily limit")
	wrapped := fmt.Errorf("outer: %w", inner)
	assert.ErrorIs(t, wrapped, apperror.ErrDailyLimitExceeded)
}
