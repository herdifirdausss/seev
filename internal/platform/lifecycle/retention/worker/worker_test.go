package retentionworker

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

// expectHoldsGaugeQueries sets up the 8 scope/status COUNT(*) queries
// RunOnce's refreshHoldsGauge always issues once per call, after every
// class — every RunOnce test below must expect these too, or sqlmock
// reports them as unexpected (see refreshHoldsGauge's own doc comment).
func expectHoldsGaugeQueries(mock sqlmock.Sqlmock, table string) {
	for _, scope := range holdScopes {
		for _, status := range holdStatuses {
			mock.ExpectQuery(`SELECT count\(\*\) FROM `+table+` WHERE scope = \$1 AND status = \$2`).
				WithArgs(scope, status).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		}
	}
}

func TestNewRunner_RejectsInvalidFunctionName(t *testing.T) {
	db, _ := newMock(t)
	_, err := NewRunner("ledger", db, []Class{
		{Name: "ledger.fee_quotes.unconsumed", Action: "delete", FunctionName: "DROP TABLE users; --"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid FunctionName")
}

func TestNewRunner_RejectsDuplicateClassName(t *testing.T) {
	db, _ := newMock(t)
	class := Class{Name: "ledger.fee_quotes.unconsumed", Action: "delete", FunctionName: "fn_retention_purge_fee_quotes_unconsumed"}
	_, err := NewRunner("ledger", db, []Class{class, class})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate class")
}

func TestNewRunner_RejectsMissingOwnerOrDB(t *testing.T) {
	db, _ := newMock(t)
	_, err := NewRunner("", db, nil)
	require.Error(t, err)

	_, err = NewRunner("ledger", nil, nil)
	require.Error(t, err)
}

func TestRunOnce_RealRun_LoopsUntilBatchUndersized(t *testing.T) {
	db, mock := newMock(t)
	class := Class{Name: "ledger.fee_quotes.unconsumed", Action: "delete", FunctionName: "fn_retention_purge_fee_quotes_unconsumed"}
	r, err := NewRunner("ledger", db, []Class{class}, WithBatchSize(2))
	require.NoError(t, err)

	// Three calls: 2 affected, 2 affected, 1 affected (< batchSize) — stop.
	mock.ExpectQuery(`SELECT fn_retention_purge_fee_quotes_unconsumed\(\$1, \$2, \$3\)`).
		WithArgs(sqlmock.AnyArg(), 2, false).
		WillReturnRows(sqlmock.NewRows([]string{"fn_retention_purge_fee_quotes_unconsumed"}).AddRow(2))
	mock.ExpectQuery(`SELECT fn_retention_purge_fee_quotes_unconsumed\(\$1, \$2, \$3\)`).
		WithArgs(sqlmock.AnyArg(), 2, false).
		WillReturnRows(sqlmock.NewRows([]string{"fn_retention_purge_fee_quotes_unconsumed"}).AddRow(2))
	mock.ExpectQuery(`SELECT fn_retention_purge_fee_quotes_unconsumed\(\$1, \$2, \$3\)`).
		WithArgs(sqlmock.AnyArg(), 2, false).
		WillReturnRows(sqlmock.NewRows([]string{"fn_retention_purge_fee_quotes_unconsumed"}).AddRow(1))

	expectHoldsGaugeQueries(mock, "ledger_retention_holds")
	report := r.RunOnce(context.Background(), false)
	result := report.Classes[class.Name]
	require.NoError(t, result.Err)
	assert.Equal(t, 5, result.Affected)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRunOnce_DryRun_CallsExactlyOnce(t *testing.T) {
	db, mock := newMock(t)
	class := Class{Name: "ledger.fee_quotes.unconsumed", Action: "delete", FunctionName: "fn_retention_purge_fee_quotes_unconsumed"}
	r, err := NewRunner("ledger", db, []Class{class}, WithBatchSize(2))
	require.NoError(t, err)

	// A dry run returns the FULL eligible count in one call (see Class's
	// doc comment) — even though it's larger than batchSize, RunOnce must
	// not call again.
	mock.ExpectQuery(`SELECT fn_retention_purge_fee_quotes_unconsumed\(\$1, \$2, \$3\)`).
		WithArgs(sqlmock.AnyArg(), 2, true).
		WillReturnRows(sqlmock.NewRows([]string{"fn_retention_purge_fee_quotes_unconsumed"}).AddRow(9000))

	expectHoldsGaugeQueries(mock, "ledger_retention_holds")
	report := r.RunOnce(context.Background(), true)
	result := report.Classes[class.Name]
	require.NoError(t, result.Err)
	assert.Equal(t, 9000, result.Affected)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRunOnce_StopsAtPerRunCap(t *testing.T) {
	db, mock := newMock(t)
	class := Class{Name: "ledger.fee_quotes.unconsumed", Action: "delete", FunctionName: "fn_retention_purge_fee_quotes_unconsumed"}
	r, err := NewRunner("ledger", db, []Class{class}, WithBatchSize(10), WithPerRunCap(25))
	require.NoError(t, err)

	// Every call returns a full batch (10) — an unbounded backlog. The
	// per-run cap (25) must still stop the loop after 3 calls (30 >= 25),
	// not run forever.
	for range 3 {
		mock.ExpectQuery(`SELECT fn_retention_purge_fee_quotes_unconsumed\(\$1, \$2, \$3\)`).
			WithArgs(sqlmock.AnyArg(), 10, false).
			WillReturnRows(sqlmock.NewRows([]string{"fn_retention_purge_fee_quotes_unconsumed"}).AddRow(10))
	}

	expectHoldsGaugeQueries(mock, "ledger_retention_holds")
	report := r.RunOnce(context.Background(), false)
	result := report.Classes[class.Name]
	require.NoError(t, result.Err)
	assert.Equal(t, 30, result.Affected)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRunOnce_RefreshesHoldsGauge proves refreshHoldsGauge queries every
// scope/status combination and sets seev_retention_holds accordingly — a
// query failure for one combination must not stop the others from being
// refreshed, matching the same "one failure doesn't block the rest"
// philosophy as the class loop itself.
func TestRunOnce_RefreshesHoldsGauge(t *testing.T) {
	db, mock := newMock(t)
	class := Class{Name: "ledger.fee_quotes.unconsumed", Action: "delete", FunctionName: "fn_retention_purge_fee_quotes_unconsumed"}
	r, err := NewRunner("ledger", db, []Class{class})
	require.NoError(t, err)

	mock.ExpectQuery(`SELECT fn_retention_purge_fee_quotes_unconsumed\(\$1, \$2, \$3\)`).
		WithArgs(sqlmock.AnyArg(), DefaultBatchSize, true).
		WillReturnRows(sqlmock.NewRows([]string{"fn_retention_purge_fee_quotes_unconsumed"}).AddRow(0))

	for _, scope := range holdScopes {
		for _, status := range holdStatuses {
			q := mock.ExpectQuery(`SELECT count\(\*\) FROM ledger_retention_holds WHERE scope = \$1 AND status = \$2`).
				WithArgs(scope, status)
			if scope == "subject" && status == "active" {
				q.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
			} else if scope == "resource" && status == "active" {
				q.WillReturnError(errors.New("connection reset"))
			} else {
				q.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			}
		}
	}

	r.RunOnce(context.Background(), true)
	require.NoError(t, mock.ExpectationsWereMet(), "every scope/status combination must still be queried even after one fails")

	metric := &dto.Metric{}
	require.NoError(t, holdsGauge.WithLabelValues("ledger", "subject", "active").Write(metric))
	assert.Equal(t, float64(3), metric.GetGauge().GetValue())
}

func TestRunOnce_OneClassFailureDoesNotStopOthers(t *testing.T) {
	db, mock := newMock(t)
	broken := Class{Name: "ledger.broken", Action: "delete", FunctionName: "fn_retention_purge_broken"}
	healthy := Class{Name: "ledger.healthy", Action: "delete", FunctionName: "fn_retention_purge_healthy"}
	r, err := NewRunner("ledger", db, []Class{broken, healthy}, WithBatchSize(500))
	require.NoError(t, err)

	mock.ExpectQuery(`SELECT fn_retention_purge_broken\(\$1, \$2, \$3\)`).
		WillReturnError(errors.New("connection reset"))
	mock.ExpectQuery(`SELECT fn_retention_purge_healthy\(\$1, \$2, \$3\)`).
		WithArgs(sqlmock.AnyArg(), 500, false).
		WillReturnRows(sqlmock.NewRows([]string{"fn_retention_purge_healthy"}).AddRow(3))

	expectHoldsGaugeQueries(mock, "ledger_retention_holds")
	report := r.RunOnce(context.Background(), false)
	require.Error(t, report.Classes[broken.Name].Err)
	require.NoError(t, report.Classes[healthy.Name].Err)
	assert.Equal(t, 3, report.Classes[healthy.Name].Affected)
	require.NoError(t, mock.ExpectationsWereMet())
}
