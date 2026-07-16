package database

import (
	"context"
	"database/sql"
)

// MockDatabaseSQL is a test double for the Database interface.
// Each method is backed by a function field; if nil, the method is a no-op.
//
// Usage in tests:
//
//	db := &database.MockDatabaseSQL{
//	    HealthCheckFn: func(ctx context.Context) error { return nil },
//	}
type MockDatabaseSQL struct {
	QueryContextFn    func(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContextFn func(ctx context.Context, query string, args ...any) *sql.Row
	ExecContextFn     func(ctx context.Context, query string, args ...any) (sql.Result, error)
	WithTxFn          func(ctx context.Context, opts *sql.TxOptions, fn func(*sql.Tx) error) error
	HealthCheckFn     func(ctx context.Context) error
	CloseFn           func() error
	StatsFn           func() sql.DBStats
}

// Compile-time interface compliance check.
var _ DatabaseSQL = (*MockDatabaseSQL)(nil)

func (m *MockDatabaseSQL) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if m.QueryContextFn != nil {
		return m.QueryContextFn(ctx, query, args...)
	}
	return nil, nil
}

func (m *MockDatabaseSQL) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if m.QueryRowContextFn != nil {
		return m.QueryRowContextFn(ctx, query, args...)
	}
	return nil
}

func (m *MockDatabaseSQL) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if m.ExecContextFn != nil {
		return m.ExecContextFn(ctx, query, args...)
	}
	return nil, nil
}

func (m *MockDatabaseSQL) WithTx(ctx context.Context, opts *sql.TxOptions, fn func(*sql.Tx) error) error {
	if m.WithTxFn != nil {
		return m.WithTxFn(ctx, opts, fn)
	}
	return nil
}

func (m *MockDatabaseSQL) HealthCheck(ctx context.Context) error {
	if m.HealthCheckFn != nil {
		return m.HealthCheckFn(ctx)
	}
	return nil
}

func (m *MockDatabaseSQL) Close() error {
	if m.CloseFn != nil {
		return m.CloseFn()
	}
	return nil
}

func (m *MockDatabaseSQL) Stats() sql.DBStats {
	if m.StatsFn != nil {
		return m.StatsFn()
	}
	return sql.DBStats{}
}
