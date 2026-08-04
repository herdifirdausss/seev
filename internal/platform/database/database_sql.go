package database

import (
	"context"
	"database/sql"
)

// DatabaseSQL is the interface that wraps all database operations.
// All application code depending on a database must use this interface,
// making it trivial to swap in a MockDatabase during unit tests.
//
//mockery:generate: true
//mockery:structname: MockFoo
type DatabaseSQL interface {
	// QueryContext executes a query that returns rows.
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)

	// QueryRowContext executes a query that returns at most one row.
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row

	// ExecContext executes a query without returning rows.
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)

	// WithTx runs fn inside a database transaction.
	// Automatically rolls back on error or panic; commits on success.
	WithTx(ctx context.Context, opts *sql.TxOptions, fn func(*sql.Tx) error) error

	// HealthCheck verifies the database is reachable.
	HealthCheck(ctx context.Context) error

	// Close releases all database connections.
	Close() error

	// Stats returns current connection pool statistics.
	Stats() sql.DBStats
}
