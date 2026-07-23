package repository

//go:generate mockgen -source=snapshot_repository.go -destination=snapshot_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/pkg/database"
)

// dateLayout is the format account_balance_snapshots.as_of_date is passed
// as — Postgres's ::date cast accepts it directly, and it makes the SQL
// unambiguous about which calendar day (in Asia/Jakarta) is meant,
// independent of the time.Time value's own location.
const dateLayout = "2006-01-02"

// BalanceMismatch is one row where a snapshot's closing_balance disagrees
// with the account's current stored balance, for an account with no entries
// since that snapshot — i.e. a real projection corruption, not just a
// snapshot that predates recent activity (docs/roadmap/archive/15 Task T1).
type BalanceMismatch struct {
	AccountID       uuid.UUID
	AsOfDate        time.Time
	SnapshotBalance decimal.Decimal
	CurrentBalance  decimal.Decimal
}

// SnapshotRepository persists and reads daily account balance snapshots
// (docs/roadmap/archive/15 Task T1, decision K6) — a cheap way to answer "what was this
// account's balance on date X" and "opening balance for a statement period"
// without replaying every ledger_entries row since the account was created.
type SnapshotRepository interface {
	// InsertForDate computes and stores closing_balance for every account
	// that had at least one ledger_entries row on date (Asia/Jakarta calendar
	// day), in a single set-based INSERT. Accounts with no activity that day
	// get no row — readers walk back via GetLatestBefore instead. Safe to
	// call more than once for the same date (ON CONFLICT DO NOTHING) — the
	// daily job's own retry-on-restart doesn't double-write. Returns the
	// number of accounts snapshotted.
	InsertForDate(ctx context.Context, date time.Time) (int, error)

	// GetLatestBefore returns the most recent snapshot at or before date for
	// accountID. found=false if no snapshot exists yet (e.g. an account
	// younger than the snapshot job's history, or the job hasn't run yet).
	GetLatestBefore(ctx context.Context, accountID uuid.UUID, date time.Time) (balance decimal.Decimal, asOf time.Time, found bool, err error)

	// BalanceAsOf returns accountID's balance at the end of date (Asia/Jakarta
	// calendar day): the latest snapshot at or before date, plus the net delta
	// of any entries between that snapshot and the end of date. Falls back to
	// summing every entry up to date when no snapshot exists yet — correct,
	// just not the fast path.
	BalanceAsOf(ctx context.Context, accountID uuid.UUID, date time.Time) (decimal.Decimal, error)

	// VerifyDate compares every snapshot row for date against the account's
	// CURRENT stored balance, restricted to accounts with no ledger_entries
	// activity after that date's end — for those accounts the two must be
	// identical; a mismatch is a real projection bug, not just staleness.
	VerifyDate(ctx context.Context, date time.Time) ([]BalanceMismatch, error)

	// LatestSnapshotDate returns the most recent as_of_date across every
	// account, used by the daily job's startup catch-up to find which dates
	// are missing. found=false if the table is empty (fresh deployment).
	LatestSnapshotDate(ctx context.Context) (date time.Time, found bool, err error)
}

type snapshotRepo struct {
	db  database.DatabaseSQL
	loc *time.Location
}

// NewSnapshotRepository requires loc (Asia/Jakarta in production) — every
// "date" boundary in this repository is a calendar day in that location,
// not UTC, matching how the business defines "today" (docs/roadmap/archive/15 Task T1
// timezone test: an entry at 23:30 WIB and one at 00:30 WIB must land in
// different snapshot dates even though their UTC clock times are ~1 hour
// apart on the same UTC day).
func NewSnapshotRepository(db database.DatabaseSQL, loc *time.Location) SnapshotRepository {
	if loc == nil {
		loc = time.UTC
	}
	return &snapshotRepo{db: db, loc: loc}
}

func (r *snapshotRepo) InsertForDate(ctx context.Context, date time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO account_balance_snapshots (account_id, as_of_date, closing_balance, entry_count)
		SELECT e.account_id, $1::date,
		       (SELECT balance_after FROM ledger_entries le
		         WHERE le.account_id = e.account_id
		           AND le.created_at < (($1::date + 1)::timestamp AT TIME ZONE $2)
		         ORDER BY le.created_at DESC, le.id DESC LIMIT 1),
		       count(*)
		FROM ledger_entries e
		WHERE e.created_at >= ($1::date::timestamp AT TIME ZONE $2)
		  AND e.created_at <  (($1::date + 1)::timestamp AT TIME ZONE $2)
		GROUP BY e.account_id
		ON CONFLICT (account_id, as_of_date) DO NOTHING`,
		date.Format(dateLayout), r.loc.String(),
	)
	if err != nil {
		return 0, fmt.Errorf("insert snapshot for %s: %w", date.Format(dateLayout), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("insert snapshot for %s: rows affected: %w", date.Format(dateLayout), err)
	}
	return int(n), nil
}

func (r *snapshotRepo) GetLatestBefore(ctx context.Context, accountID uuid.UUID, date time.Time) (decimal.Decimal, time.Time, bool, error) {
	var balance int64
	var asOf time.Time
	err := r.db.QueryRowContext(ctx, `
		SELECT closing_balance, as_of_date
		FROM account_balance_snapshots
		WHERE account_id = $1 AND as_of_date <= $2::date
		ORDER BY as_of_date DESC
		LIMIT 1`,
		accountID, date.Format(dateLayout),
	).Scan(&balance, &asOf)
	if errors.Is(err, sql.ErrNoRows) {
		return decimal.Zero, time.Time{}, false, nil
	}
	if err != nil {
		return decimal.Zero, time.Time{}, false, fmt.Errorf("get latest snapshot before %s: %w", date.Format(dateLayout), err)
	}
	return decimal.NewFromInt(balance), asOf, true, nil
}

func (r *snapshotRepo) BalanceAsOf(ctx context.Context, accountID uuid.UUID, date time.Time) (decimal.Decimal, error) {
	baseline, snapshotDate, found, err := r.GetLatestBefore(ctx, accountID, date)
	if err != nil {
		return decimal.Zero, err
	}

	// deltaFrom is exclusive — entries strictly after the snapshot's own
	// calendar day (the snapshot already accounts for everything through
	// end of snapshotDate). No snapshot found -> "-infinity", Postgres's
	// date special value, so `deltaFrom::date + 1` and the AT TIME ZONE
	// conversion both propagate -infinity and the lower bound becomes
	// always-true — summing the account's entire history up to date
	// (correct fallback, just not the fast path). No CASE needed: Postgres
	// date/timestamp infinity arithmetic does the right thing natively.
	deltaFrom := "-infinity"
	if found {
		deltaFrom = snapshotDate.Format(dateLayout)
	}

	var delta sql.NullInt64
	err = r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN direction = 'credit' THEN amount ELSE -amount END), 0)
		FROM ledger_entries
		WHERE account_id = $1
		  AND created_at >= (($2::date + 1)::timestamp AT TIME ZONE $4)
		  AND created_at <  (($3::date + 1)::timestamp AT TIME ZONE $4)`,
		accountID, deltaFrom, date.Format(dateLayout), r.loc.String(),
	).Scan(&delta)
	if err != nil {
		return decimal.Zero, fmt.Errorf("balance as of %s: delta: %w", date.Format(dateLayout), err)
	}

	return baseline.Add(decimal.NewFromInt(delta.Int64)), nil
}

func (r *snapshotRepo) VerifyDate(ctx context.Context, date time.Time) ([]BalanceMismatch, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.account_id, s.as_of_date, s.closing_balance, ab.balance
		FROM account_balance_snapshots s
		JOIN account_balances ab ON ab.account_id = s.account_id
		WHERE s.as_of_date = $1::date
		  AND ab.updated_at < (($1::date + 1)::timestamp AT TIME ZONE $2)
		  AND s.closing_balance <> ab.balance`,
		date.Format(dateLayout), r.loc.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("verify snapshot for %s: %w", date.Format(dateLayout), err)
	}
	defer rows.Close()

	var mismatches []BalanceMismatch
	for rows.Next() {
		var m BalanceMismatch
		var snapshotBalance, currentBalance int64
		if err := rows.Scan(&m.AccountID, &m.AsOfDate, &snapshotBalance, &currentBalance); err != nil {
			return nil, fmt.Errorf("scan mismatch: %w", err)
		}
		m.SnapshotBalance = decimal.NewFromInt(snapshotBalance)
		m.CurrentBalance = decimal.NewFromInt(currentBalance)
		mismatches = append(mismatches, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mismatches: %w", err)
	}
	return mismatches, nil
}

func (r *snapshotRepo) LatestSnapshotDate(ctx context.Context) (time.Time, bool, error) {
	// max() over an empty table returns ONE row with a NULL value, not zero
	// rows — sql.ErrNoRows never fires here. sql.NullTime is what correctly
	// distinguishes "no snapshots yet" from a real date (caught by the T1
	// Docker smoke test: scanning NULL straight into *time.Time errors with
	// "unsupported Scan ... storing driver.Value type <nil>").
	var date sql.NullTime
	err := r.db.QueryRowContext(ctx, `SELECT max(as_of_date) FROM account_balance_snapshots`).Scan(&date)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("latest snapshot date: %w", err)
	}
	if !date.Valid {
		return time.Time{}, false, nil
	}
	return date.Time, true, nil
}
