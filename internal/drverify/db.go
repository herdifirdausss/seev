package drverify

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// connect opens a bounded connection pool for one service's DSN. Kept
// deliberately small (drverify makes one full pass, not a live service's
// worth of concurrent traffic) — K9 "bounded concurrency" applies to
// pool size too, not just goroutine count.
func connect(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return db, nil
}

// readOnlyQuery runs fn inside a genuinely read-only transaction
// (sql.TxOptions{ReadOnly: true} — Postgres itself rejects any write
// statement inside it, not just an application-level convention) with
// SET LOCAL statement_timeout/lock_timeout applied first (K9 "bounded
// ... statement timeouts", applied via SET LOCAL rather than baked into
// the DSN since an operator-supplied DSN's format is not guaranteed).
func readOnlyQuery(ctx context.Context, db *sql.DB, cfg Config, fn func(ctx context.Context, tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin read-only tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", cfg.StatementTimeout.Milliseconds())); err != nil {
		return fmt.Errorf("set statement_timeout: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL lock_timeout = %d", cfg.LockTimeout.Milliseconds())); err != nil {
		return fmt.Errorf("set lock_timeout: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	// Always rolled back (deferred above) even on success — a read-only
	// transaction has nothing to commit, and rollback is the one path
	// that can never be mistaken for accidentally persisting a write.
	return nil
}
