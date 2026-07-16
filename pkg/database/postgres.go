package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// DBSQL is the production implementation of the Database interface.
// It wraps *sql.DBSQL with health-check, transaction helpers, and structured logging.
type DBSQL struct {
	db  *sql.DB
	cfg Config
}

// Compile-time interface compliance check.
var _ DatabaseSQL = (*DBSQL)(nil)

// New opens a PostgreSQL connection pool and validates connectivity.
// Pool is configured from cfg; returns an error if the database is unreachable.
func New(ctx context.Context, cfg Config) (*DBSQL, error) {
	sqlDB, err := sql.Open("pgx", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	slog.Info("postgres: connected",
		"host", cfg.Host, "port", cfg.Port, "db", cfg.DB,
		"max_open", cfg.MaxOpenConns, "max_idle", cfg.MaxIdleConns,
	)

	return &DBSQL{db: sqlDB, cfg: cfg}, nil
}

// NewFromSQL wraps an existing *sql.DB — useful for testing with sqlmock.
func NewFromSQL(sqlDB *sql.DB, cfg Config) *DBSQL {
	return &DBSQL{db: sqlDB, cfg: cfg}
}

func (d *DBSQL) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.db.QueryContext(ctx, query, args...)
}

func (d *DBSQL) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.db.QueryRowContext(ctx, query, args...)
}

func (d *DBSQL) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.db.ExecContext(ctx, query, args...)
}

// HealthCheck verifies the database is reachable with a lightweight query.
func (d *DBSQL) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if err := d.db.PingContext(ctx); err != nil {
		return fmt.Errorf("postgres: ping: %w", err)
	}

	var result int
	if err := d.db.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil {
		return fmt.Errorf("postgres: health query: %w", err)
	}

	return nil
}

// Close releases all connections in the pool.
func (d *DBSQL) Close() error {
	slog.Info("postgres: closing pool")
	return d.db.Close()
}

// Stats returns current pool metrics.
func (d *DBSQL) Stats() sql.DBStats {
	return d.db.Stats()
}

// WithTx runs fn inside a database transaction.
// Panics are recovered and cause a rollback before re-panicking.
func (d *DBSQL) WithTx(ctx context.Context, opts *sql.TxOptions, fn func(*sql.Tx) error) error {
	tx, err := d.db.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			slog.Error("tx rollback failed", "error", rbErr, "original", err)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

// ─── generalutility ──────────────────────────────────────────────────────────────────

// Paginate appends LIMIT and OFFSET clauses.
// page is 1-indexed; size is capped to maxSize.
func Paginate(query string, page, size, maxSize int) (paged string, limit, offset int) {
	if size <= 0 || size > maxSize {
		size = maxSize
	}
	if page <= 0 {
		page = 1
	}
	offset = (page - 1) * size
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", query, size, offset), size, offset
}
