package policy

//go:generate mockgen -source=repository.go -destination=repository_mock.go -package=policy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// Repository persists policy_limits (docs/roadmap/archive/17 Task T1).
type Repository interface {
	// GetEffective returns the limit that applies to userID+txType: a
	// user-specific override if one exists, else the type-wide default
	// (user_id IS NULL), else found=false (unbounded — no row configured).
	GetEffective(ctx context.Context, userID uuid.UUID, txType string) (limit Limit, found bool, err error)

	// Upsert inserts or updates by (user_id, transaction_type).
	Upsert(ctx context.Context, l Limit) (Limit, error)

	// List returns limits, optionally filtered by transaction_type (empty
	// = all) and userID (nil = all, including defaults).
	List(ctx context.Context, txType string, userID *uuid.UUID) ([]Limit, error)
}

type repo struct {
	db database.DatabaseSQL
}

func NewRepository(db database.DatabaseSQL) Repository {
	return &repo{db: db}
}

func (r *repo) GetEffective(ctx context.Context, userID uuid.UUID, txType string) (Limit, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, transaction_type, max_per_tx, max_daily_amount,
		       max_daily_count, max_monthly_amount, enabled, created_at, updated_at
		FROM policy_limits
		WHERE transaction_type = $1 AND (user_id = $2 OR user_id IS NULL)
		ORDER BY user_id NULLS LAST
		LIMIT 1`,
		txType, userID,
	)
	l, err := scanLimit(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Limit{}, false, nil
	}
	if err != nil {
		return Limit{}, false, fmt.Errorf("get effective policy limit: %w", err)
	}
	return l, true, nil
}

func (r *repo) Upsert(ctx context.Context, l Limit) (Limit, error) {
	if l.ID == uuid.Nil {
		l.ID = generalutil.NewV7()
	}
	// Two different arbiter indexes depending on whether this is a
	// type-wide default (user_id NULL) or a user-specific override
	// (user_id set) — Postgres's plain UNIQUE(user_id, transaction_type)
	// constraint never fires for NULL user_id (NULLs are distinct from
	// each other in a unique constraint by SQL standard), which is exactly
	// why the default case has its own partial index
	// (uq_policy_limits_default). ON CONFLICT can only target ONE arbiter
	// per statement, so this has to be two statements, not one with a
	// combined WHERE.
	var row *sql.Row
	if l.UserID == nil {
		row = r.db.QueryRowContext(ctx, `
			INSERT INTO policy_limits
				(id, user_id, transaction_type, max_per_tx, max_daily_amount,
				 max_daily_count, max_monthly_amount, enabled)
			VALUES ($1,NULL,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (transaction_type) WHERE user_id IS NULL DO UPDATE SET
				max_per_tx = EXCLUDED.max_per_tx,
				max_daily_amount = EXCLUDED.max_daily_amount,
				max_daily_count = EXCLUDED.max_daily_count,
				max_monthly_amount = EXCLUDED.max_monthly_amount,
				enabled = EXCLUDED.enabled
			RETURNING id, user_id, transaction_type, max_per_tx, max_daily_amount,
			          max_daily_count, max_monthly_amount, enabled, created_at, updated_at`,
			l.ID, l.TransactionType, l.MaxPerTx, l.MaxDailyAmount,
			l.MaxDailyCount, l.MaxMonthlyAmount, l.Enabled,
		)
	} else {
		row = r.db.QueryRowContext(ctx, `
			INSERT INTO policy_limits
				(id, user_id, transaction_type, max_per_tx, max_daily_amount,
				 max_daily_count, max_monthly_amount, enabled)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (user_id, transaction_type) DO UPDATE SET
				max_per_tx = EXCLUDED.max_per_tx,
				max_daily_amount = EXCLUDED.max_daily_amount,
				max_daily_count = EXCLUDED.max_daily_count,
				max_monthly_amount = EXCLUDED.max_monthly_amount,
				enabled = EXCLUDED.enabled
			RETURNING id, user_id, transaction_type, max_per_tx, max_daily_amount,
			          max_daily_count, max_monthly_amount, enabled, created_at, updated_at`,
			l.ID, *l.UserID, l.TransactionType, l.MaxPerTx, l.MaxDailyAmount,
			l.MaxDailyCount, l.MaxMonthlyAmount, l.Enabled,
		)
	}
	out, err := scanLimit(row)
	if err != nil {
		return Limit{}, fmt.Errorf("upsert policy limit: %w", err)
	}
	return out, nil
}

func (r *repo) List(ctx context.Context, txType string, userID *uuid.UUID) ([]Limit, error) {
	var userIDArg any
	if userID != nil {
		userIDArg = *userID
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, transaction_type, max_per_tx, max_daily_amount,
		       max_daily_count, max_monthly_amount, enabled, created_at, updated_at
		FROM policy_limits
		WHERE ($1 = '' OR transaction_type = $1)
		  AND ($2::uuid IS NULL OR user_id = $2)
		ORDER BY transaction_type, user_id NULLS FIRST`,
		txType, userIDArg,
	)
	if err != nil {
		return nil, fmt.Errorf("list policy limits: %w", err)
	}
	defer rows.Close()

	var out []Limit
	for rows.Next() {
		l, err := scanLimitRows(rows)
		if err != nil {
			return nil, fmt.Errorf("scan policy limit: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate policy limits: %w", err)
	}
	return out, nil
}

// rowScanner abstracts *sql.Row and *sql.Rows — both expose Scan.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanLimit(row *sql.Row) (Limit, error)       { return scanRow(row) }
func scanLimitRows(rows *sql.Rows) (Limit, error) { return scanRow(rows) }

func scanRow(s rowScanner) (Limit, error) {
	var (
		l         Limit
		userID    uuid.NullUUID
		maxPerTx  sql.NullInt64
		maxDailyA sql.NullInt64
		maxDailyC sql.NullInt32
		maxMonA   sql.NullInt64
	)
	err := s.Scan(&l.ID, &userID, &l.TransactionType, &maxPerTx, &maxDailyA,
		&maxDailyC, &maxMonA, &l.Enabled, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return Limit{}, err
	}
	if userID.Valid {
		id := userID.UUID
		l.UserID = &id
	}
	if maxPerTx.Valid {
		v := maxPerTx.Int64
		l.MaxPerTx = &v
	}
	if maxDailyA.Valid {
		v := maxDailyA.Int64
		l.MaxDailyAmount = &v
	}
	if maxDailyC.Valid {
		v := maxDailyC.Int32
		l.MaxDailyCount = &v
	}
	if maxMonA.Valid {
		v := maxMonA.Int64
		l.MaxMonthlyAmount = &v
	}
	return l, nil
}
