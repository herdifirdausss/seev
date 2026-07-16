package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newVerifierTestDB(t *testing.T) (*database.DBSQL, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	return database.NewFromSQL(sqlDB, config.PostgresConfig{Host: "localhost"}.Pkg()), mock
}

func TestVerifier_CheckTrialBalance_NoDiscrepancies(t *testing.T) {
	db, mock := newVerifierTestDB(t)
	v := &Verifier{db: db, logger: discardLogger()}

	mock.ExpectQuery(`fn_verify_ledger_balance`).
		WillReturnRows(sqlmock.NewRows([]string{"transaction_id", "sum_debit", "sum_credit", "diff"}))

	err := v.checkTrialBalance(context.Background())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVerifier_CheckTrialBalance_FindsDiscrepancy(t *testing.T) {
	db, mock := newVerifierTestDB(t)
	v := &Verifier{db: db, logger: discardLogger()}

	mock.ExpectQuery(`fn_verify_ledger_balance`).
		WillReturnRows(sqlmock.NewRows([]string{"transaction_id", "sum_debit", "sum_credit", "diff"}).
			AddRow("11111111-1111-1111-1111-111111111111", 100, 90, 10))

	err := v.checkTrialBalance(context.Background())
	require.NoError(t, err) // detection, not repair — must not error the job
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVerifier_CheckProjectionAudit_FindsInconsistency(t *testing.T) {
	db, mock := newVerifierTestDB(t)
	v := &Verifier{db: db, logger: discardLogger()}

	mock.ExpectQuery(`v_account_balance_audit`).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "stored_balance", "computed_balance"}).
			AddRow("22222222-2222-2222-2222-222222222222", 500, 400))

	err := v.checkProjectionAudit(context.Background())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ─── Alert hook (docs/plan/12 Task T4) ─────────────────────────────────────────

func TestVerifier_CheckTrialBalance_AlertFnCalledOncePerDiscrepancy(t *testing.T) {
	db, mock := newVerifierTestDB(t)

	var calls []struct{ severity, message string }
	v := &Verifier{db: db, logger: discardLogger(), alertFn: func(_ context.Context, severity, message string) error {
		calls = append(calls, struct{ severity, message string }{severity, message})
		return nil
	}}

	mock.ExpectQuery(`fn_verify_ledger_balance`).
		WillReturnRows(sqlmock.NewRows([]string{"transaction_id", "sum_debit", "sum_credit", "diff"}).
			AddRow("11111111-1111-1111-1111-111111111111", 100, 90, 10).
			AddRow("22222222-2222-2222-2222-222222222222", 50, 50, 0))

	err := v.checkTrialBalance(context.Background())
	require.NoError(t, err)
	require.Len(t, calls, 2, "alertFn must fire once per discrepancy row")
	for _, c := range calls {
		require.Equal(t, "critical", c.severity)
		require.Contains(t, c.message, "unbalanced transaction")
	}
	require.Contains(t, calls[0].message, "11111111-1111-1111-1111-111111111111")
}

func TestVerifier_CheckProjectionAudit_AlertFnCalledWithDetails(t *testing.T) {
	db, mock := newVerifierTestDB(t)

	var gotSeverity, gotMessage string
	v := &Verifier{db: db, logger: discardLogger(), alertFn: func(_ context.Context, severity, message string) error {
		gotSeverity, gotMessage = severity, message
		return nil
	}}

	mock.ExpectQuery(`v_account_balance_audit`).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "stored_balance", "computed_balance"}).
			AddRow("22222222-2222-2222-2222-222222222222", 500, 400))

	err := v.checkProjectionAudit(context.Background())
	require.NoError(t, err)
	require.Equal(t, "critical", gotSeverity)
	require.Contains(t, gotMessage, "22222222-2222-2222-2222-222222222222")
	require.Contains(t, gotMessage, "500")
	require.Contains(t, gotMessage, "400")
}

func TestVerifier_CheckTrialBalance_NoDiscrepancy_AlertFnNeverCalled(t *testing.T) {
	db, mock := newVerifierTestDB(t)

	called := false
	v := &Verifier{db: db, logger: discardLogger(), alertFn: func(context.Context, string, string) error {
		called = true
		return nil
	}}

	mock.ExpectQuery(`fn_verify_ledger_balance`).
		WillReturnRows(sqlmock.NewRows([]string{"transaction_id", "sum_debit", "sum_credit", "diff"}))

	require.NoError(t, v.checkTrialBalance(context.Background()))
	require.False(t, called)
}

func TestVerifier_AlertFnError_LoggedNotPropagated(t *testing.T) {
	db, mock := newVerifierTestDB(t)

	v := &Verifier{db: db, logger: discardLogger(), alertFn: func(context.Context, string, string) error {
		return errors.New("webhook unreachable")
	}}

	mock.ExpectQuery(`fn_verify_ledger_balance`).
		WillReturnRows(sqlmock.NewRows([]string{"transaction_id", "sum_debit", "sum_credit", "diff"}).
			AddRow("11111111-1111-1111-1111-111111111111", 100, 90, 10))

	// A failing alert delivery must not fail the check itself — the
	// verifier's own detection is the primary job, alerting is secondary.
	err := v.checkTrialBalance(context.Background())
	require.NoError(t, err)
}

func TestVerifier_NilAlertFn_NoPanic(t *testing.T) {
	db, mock := newVerifierTestDB(t)
	v := &Verifier{db: db, logger: discardLogger(), alertFn: nil}

	mock.ExpectQuery(`fn_verify_ledger_balance`).
		WillReturnRows(sqlmock.NewRows([]string{"transaction_id", "sum_debit", "sum_credit", "diff"}).
			AddRow("11111111-1111-1111-1111-111111111111", 100, 90, 10))

	require.NotPanics(t, func() {
		require.NoError(t, v.checkTrialBalance(context.Background()))
	})
}

func TestVerifier_CheckOutboxLag_BelowThreshold_NoAlert(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := repository.NewMockOutboxRepository(ctrl)
	repo.EXPECT().CountPending(gomock.Any()).Return(1, time.Now().Add(-1*time.Minute), nil)

	v := &Verifier{outboxRepo: repo, logger: discardLogger()}
	require.NoError(t, v.checkOutboxLag(context.Background()))
}

func TestVerifier_CheckOutboxLag_AboveThreshold_Alerts(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := repository.NewMockOutboxRepository(ctrl)
	repo.EXPECT().CountPending(gomock.Any()).Return(5, time.Now().Add(-10*time.Minute), nil)

	v := &Verifier{outboxRepo: repo, logger: discardLogger()}
	require.NoError(t, v.checkOutboxLag(context.Background())) // alert path must not error the job
}

func TestVerifier_CheckOutboxLag_NoPending_NoOp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := repository.NewMockOutboxRepository(ctrl)
	repo.EXPECT().CountPending(gomock.Any()).Return(0, time.Time{}, nil)

	v := &Verifier{outboxRepo: repo, logger: discardLogger()}
	require.NoError(t, v.checkOutboxLag(context.Background()))
}

func TestVerifier_StartRegistersAllChecks(t *testing.T) {
	db, mock := newVerifierTestDB(t)
	mock.MatchExpectationsInOrder(false)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := repository.NewMockOutboxRepository(ctrl)

	v := NewVerifier(db, repo, scheduler.NewMemoryLock(time.Second), discardLogger(), time.UTC, nil)
	require.NoError(t, v.Start())
	v.Stop()
}
