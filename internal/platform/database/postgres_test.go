package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockDB(t *testing.T) (*DBSQL, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp),
		sqlmock.MonitorPingsOption(true),
	)
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	db := NewFromSQL(sqlDB, Config{Host: "localhost"})
	return db, mock
}

// ─── Interface compliance ─────────────────────────────────────────────────────

func TestDB_ImplementsInterface(t *testing.T) {
	var _ DatabaseSQL = (*DBSQL)(nil)
}

func TestMockDatabase_ImplementsInterface(t *testing.T) {
	var _ DatabaseSQL = (*MockDatabaseSQL)(nil)
}

// ─── NewFromSQL ───────────────────────────────────────────────────────────────

func TestNewFromSQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer sqlDB.Close()

	db := NewFromSQL(sqlDB, Config{Host: "test"})
	assert.NotNil(t, db)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── QueryContext ─────────────────────────────────────────────────────────────

func TestDB_QueryContext(t *testing.T) {
	db, mock := newMockDB(t)

	rows := sqlmock.NewRows([]string{"id"}).AddRow(1)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := db.QueryContext(context.Background(), "SELECT 1")
	require.NoError(t, err)
	defer result.Close()
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDB_QueryContext_Error(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("query error"))

	_, err := db.QueryContext(context.Background(), "SELECT 1")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── QueryRowContext ──────────────────────────────────────────────────────────

func TestDB_QueryRowContext(t *testing.T) {
	db, mock := newMockDB(t)

	rows := sqlmock.NewRows([]string{"val"}).AddRow(42)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	row := db.QueryRowContext(context.Background(), "SELECT 42")
	assert.NotNil(t, row)

	var val int
	err := row.Scan(&val)
	require.NoError(t, err)
	assert.Equal(t, 42, val)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── ExecContext ──────────────────────────────────────────────────────────────

func TestDB_ExecContext(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec("INSERT").WillReturnResult(sqlmock.NewResult(1, 1))

	result, err := db.ExecContext(context.Background(), "INSERT INTO t VALUES (1)")
	require.NoError(t, err)

	affected, _ := result.RowsAffected()
	assert.Equal(t, int64(1), affected)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDB_ExecContext_Error(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec("INSERT").WillReturnError(errors.New("exec error"))

	_, err := db.ExecContext(context.Background(), "INSERT INTO t VALUES (1)")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── HealthCheck ──────────────────────────────────────────────────────────────

func TestDB_HealthCheck_Success(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectPing()
	rows := sqlmock.NewRows([]string{"val"}).AddRow(1)
	mock.ExpectQuery(`SELECT 1`).WillReturnRows(rows)

	err := db.HealthCheck(context.Background())
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDB_HealthCheck_PingFails(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectPing().WillReturnError(errors.New("ping failed"))

	err := db.HealthCheck(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ping")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDB_HealthCheck_QueryFails(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectPing()
	mock.ExpectQuery(`SELECT 1`).WillReturnError(errors.New("query failed"))

	err := db.HealthCheck(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "health query")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── Close ────────────────────────────────────────────────────────────────────

func TestDB_Close(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectClose()
	err := db.Close()
	assert.NoError(t, err)
}

// ─── Stats ────────────────────────────────────────────────────────────────────

func TestDB_Stats(t *testing.T) {
	db, _ := newMockDB(t)
	stats := db.Stats()
	assert.IsType(t, sql.DBStats{}, stats)
}

// ─── WithTx ───────────────────────────────────────────────────────────────────

func TestDB_WithTx_Commit(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := db.WithTx(context.Background(), nil, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), "INSERT INTO t VALUES (1)")
		return err
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDB_WithTx_Rollback_OnFnError(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectBegin()
	mock.ExpectRollback()

	fnErr := errors.New("fn failed")
	err := db.WithTx(context.Background(), nil, func(tx *sql.Tx) error {
		return fnErr
	})
	assert.ErrorIs(t, err, fnErr)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDB_WithTx_Rollback_OnFnError_RollbackFails(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectBegin()
	mock.ExpectRollback().WillReturnError(errors.New("rollback error"))

	err := db.WithTx(context.Background(), nil, func(tx *sql.Tx) error {
		return errors.New("fn error")
	})
	assert.Error(t, err) // Original fn error is returned
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDB_WithTx_BeginFails(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectBegin().WillReturnError(errors.New("begin failed"))

	err := db.WithTx(context.Background(), nil, func(tx *sql.Tx) error { return nil })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "begin tx")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDB_WithTx_CommitFails(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectBegin()
	mock.ExpectCommit().WillReturnError(errors.New("commit failed"))

	err := db.WithTx(context.Background(), nil, func(tx *sql.Tx) error { return nil })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "commit tx")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDB_WithTx_PanicRollsBack(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectBegin()
	mock.ExpectRollback()

	assert.Panics(t, func() {
		_ = db.WithTx(context.Background(), nil, func(tx *sql.Tx) error {
			panic("test panic")
		})
	})
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── Paginate ─────────────────────────────────────────────────────────────────

func TestPaginate(t *testing.T) {
	tests := []struct {
		name            string
		page, size, max int
		wantLimit       int
		wantOffset      int
	}{
		{"normal", 2, 10, 100, 10, 10},
		{"page1", 1, 10, 100, 10, 0},
		{"size>max caps to max", 1, 200, 50, 50, 0},
		{"size=0 caps to max", 1, 0, 50, 50, 0},
		{"page=0 treated as 1", 0, 10, 100, 10, 0},
		{"page negative treated as 1", -1, 10, 100, 10, 0},
		{"large page", 5, 10, 100, 10, 40},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, limit, offset := Paginate("SELECT * FROM t", tt.page, tt.size, tt.max)
			assert.Equal(t, tt.wantLimit, limit)
			assert.Equal(t, tt.wantOffset, offset)
			assert.Contains(t, q, "LIMIT")
			assert.Contains(t, q, "OFFSET")
		})
	}
}

// ─── MockDatabase ─────────────────────────────────────────────────────────────

func TestMockDatabase_AllMethods_DefaultNil(t *testing.T) {
	m := &MockDatabaseSQL{}
	ctx := context.Background()

	rows, err := m.QueryContext(ctx, "SELECT 1")
	assert.Nil(t, rows)
	assert.NoError(t, err)

	row := m.QueryRowContext(ctx, "SELECT 1")
	assert.Nil(t, row)

	result, err := m.ExecContext(ctx, "INSERT")
	assert.Nil(t, result)
	assert.NoError(t, err)

	err = m.WithTx(ctx, nil, func(*sql.Tx) error { return nil })
	assert.NoError(t, err)

	err = m.HealthCheck(ctx)
	assert.NoError(t, err)

	err = m.Close()
	assert.NoError(t, err)

	stats := m.Stats()
	assert.Equal(t, sql.DBStats{}, stats)
}

func TestMockDatabase_AllMethods_WithFunctions(t *testing.T) {
	wantErr := errors.New("mock error")
	wantStats := sql.DBStats{MaxOpenConnections: 25}
	ctx := context.Background()

	m := &MockDatabaseSQL{
		QueryContextFn:    func(_ context.Context, _ string, _ ...any) (*sql.Rows, error) { return nil, wantErr },
		QueryRowContextFn: func(_ context.Context, _ string, _ ...any) *sql.Row { return nil },
		ExecContextFn:     func(_ context.Context, _ string, _ ...any) (sql.Result, error) { return nil, wantErr },
		WithTxFn:          func(_ context.Context, _ *sql.TxOptions, _ func(*sql.Tx) error) error { return wantErr },
		HealthCheckFn:     func(_ context.Context) error { return wantErr },
		CloseFn:           func() error { return wantErr },
		StatsFn:           func() sql.DBStats { return wantStats },
	}

	_, err := m.QueryContext(ctx, "")
	assert.ErrorIs(t, err, wantErr)

	assert.Nil(t, m.QueryRowContext(ctx, ""))

	_, err = m.ExecContext(ctx, "")
	assert.ErrorIs(t, err, wantErr)

	err = m.WithTx(ctx, nil, nil)
	assert.ErrorIs(t, err, wantErr)

	err = m.HealthCheck(ctx)
	assert.ErrorIs(t, err, wantErr)

	err = m.Close()
	assert.ErrorIs(t, err, wantErr)

	assert.Equal(t, wantStats, m.Stats())
}
