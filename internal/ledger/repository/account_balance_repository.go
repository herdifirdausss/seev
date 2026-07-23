package repository

//go:generate mockgen -source=account_balance_repository.go -destination=account_balance_repository_mock.go -package=repository
import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/shopspring/decimal"
)

type BalanceRepository interface {
	LockBalances(
		ctx context.Context,
		tx *sql.Tx,
		accountIDs []uuid.UUID,
	) (map[uuid.UUID]model.AccountBalance, error)

	UpdateBalances(
		ctx context.Context,
		tx *sql.Tx,
		newBalances map[uuid.UUID]decimal.Decimal,
	) error

	// GetAccountFlags returns account_balances rows (including
	// AllowNegative) WITHOUT locking, for every id in ids. Used at the
	// start of a posting transaction to decide which accounts need FOR
	// UPDATE (AllowNegative=false — user accounts, need an accurate
	// pre-read for overdraft checks) versus which can skip locking
	// entirely and use ApplySystemDeltas instead (AllowNegative=true —
	// system accounts; docs/roadmap/archive/11 Task T1). Safe to read unlocked:
	// AllowNegative itself is immutable post-provisioning (never toggled
	// by application code), and the Balance/Status/Currency snapshot
	// returned here for system accounts is only ever used for structural
	// validation (status/currency) and processor interface compatibility —
	// never for balance arithmetic, which always goes through the atomic
	// UPDATE in ApplySystemDeltas instead.
	GetAccountFlags(
		ctx context.Context,
		tx *sql.Tx,
		ids []uuid.UUID,
	) (map[uuid.UUID]model.AccountBalance, error)

	// ApplySystemDeltas atomically applies a signed delta (positive =
	// net credit, negative = net debit) to each unlocked
	// (AllowNegative=true) account via `balance = balance + delta`, and
	// returns the balance AFTER the delta for each — the authoritative
	// value to use as ledger_entries.balance_after (docs/roadmap/archive/11 Task T1).
	// Must be called from within the posting transaction. Atomicity is
	// provided by Postgres's own single-row UPDATE semantics — no
	// pre-read/lock is needed because there is no floor to violate
	// (allow_negative=true) and the new value is always expressed as a
	// delta relative to whatever the current row holds, never as an
	// absolute value computed from a stale read.
	ApplySystemDeltas(
		ctx context.Context,
		tx *sql.Tx,
		deltas map[uuid.UUID]decimal.Decimal,
	) (map[uuid.UUID]decimal.Decimal, error)

	// GetBalance is a read-only, unlocked lookup for read APIs (statement,
	// balance display) — never use it on the posting path, use LockBalances
	// instead so concurrent transfers are serialized correctly.
	GetBalance(ctx context.Context, accountID uuid.UUID) (model.AccountBalance, error)
}

type balanceRepo struct {
	db database.DatabaseSQL
}

// NewBalanceRepository requires a DB handle for GetBalance's read-only
// lookups; LockBalances/UpdateBalances always run against the tx passed in.
func NewBalanceRepository(db database.DatabaseSQL) BalanceRepository {
	return &balanceRepo{db: db}
}

func (r *balanceRepo) LockBalances(
	ctx context.Context,
	tx *sql.Tx,
	ids []uuid.UUID,
) (map[uuid.UUID]model.AccountBalance, error) {
	if len(ids) == 0 {
		return make(map[uuid.UUID]model.AccountBalance), nil
	}

	ph, args := generalutil.BuildArgs(ids)

	// [FIX 2026-07-11] FOR UPDATE OF must reference the alias (ab), not the
	// real table name — PostgreSQL requires the correlation name used in the
	// FROM clause when one is present. A prior "fix" (comment used to say
	// the opposite) had this backwards; it only surfaced once run against
	// real Postgres (sqlmock doesn't validate SQL semantics), as
	// "relation \"account_balances\" in FOR UPDATE clause not found in FROM
	// clause (SQLSTATE 42P01)".
	// JOIN accounts to get currency, status, and type for structural
	// validation [FIX #14 iter2] — currency lives on accounts, not
	// account_balances (decision D3, docs/roadmap/archive/01-target-architecture.md).
	//
	// [docs/roadmap/archive/11 Task T1] Callers are expected to pass ONLY
	// AllowNegative=false (user) account ids here — system accounts skip
	// locking entirely (see GetAccountFlags/ApplySystemDeltas). This
	// function itself doesn't enforce that; it locks whatever ids it's
	// given, same as always.
	query := fmt.Sprintf(`
		SELECT ab.account_id, ab.balance, a.currency, a.status, a.type, ab.allow_negative
		FROM   account_balances ab
		JOIN   accounts a ON a.id = ab.account_id
		WHERE  ab.account_id IN (%s)
		ORDER  BY ab.account_id
		FOR UPDATE OF ab`, ph)

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lock balances: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]model.AccountBalance, len(ids))
	for rows.Next() {
		var ab model.AccountBalance
		if err := rows.Scan(&ab.AccountID, &ab.Balance, &ab.Currency, &ab.Status, &ab.Type, &ab.AllowNegative); err != nil {
			return nil, fmt.Errorf("scan balance: %w", err)
		}
		result[ab.AccountID] = ab
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate balances: %w", err)
	}
	return result, nil
}

// GetAccountFlags — see BalanceRepository interface doc.
func (r *balanceRepo) GetAccountFlags(
	ctx context.Context,
	tx *sql.Tx,
	ids []uuid.UUID,
) (map[uuid.UUID]model.AccountBalance, error) {
	if len(ids) == 0 {
		return make(map[uuid.UUID]model.AccountBalance), nil
	}

	ph, args := generalutil.BuildArgs(ids)

	// No FOR UPDATE — this is an unlocked read used only to decide the
	// locking strategy and, for system accounts, to satisfy structural
	// validation (status/currency). Runs against tx (not r.db) so it sees
	// the same MVCC snapshot as the rest of the posting transaction.
	query := fmt.Sprintf(`
		SELECT ab.account_id, ab.balance, a.currency, a.status, a.type, ab.allow_negative
		FROM   account_balances ab
		JOIN   accounts a ON a.id = ab.account_id
		WHERE  ab.account_id IN (%s)`, ph)

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get account flags: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]model.AccountBalance, len(ids))
	for rows.Next() {
		var ab model.AccountBalance
		if err := rows.Scan(&ab.AccountID, &ab.Balance, &ab.Currency, &ab.Status, &ab.Type, &ab.AllowNegative); err != nil {
			return nil, fmt.Errorf("scan account flags: %w", err)
		}
		result[ab.AccountID] = ab
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account flags: %w", err)
	}
	return result, nil
}

// ApplySystemDeltas — see BalanceRepository interface doc. One round trip
// per account: the number of system accounts touched by a single
// transaction is always small (1-2 — a settlement/fee account, rarely
// more), so this doesn't need InsertEntries-style batching (docs/roadmap/archive/11
// Task T2 is about a different, genuinely large N).
func (r *balanceRepo) ApplySystemDeltas(
	ctx context.Context,
	tx *sql.Tx,
	deltas map[uuid.UUID]decimal.Decimal,
) (map[uuid.UUID]decimal.Decimal, error) {
	if len(deltas) == 0 {
		return make(map[uuid.UUID]decimal.Decimal), nil
	}

	ids := generalutil.SortedDecimalKeys(deltas)
	result := make(map[uuid.UUID]decimal.Decimal, len(ids))

	for _, id := range ids {
		delta := deltas[id]
		if !delta.Equal(delta.Truncate(0)) {
			// Same invariant as UpdateBalances (docs/roadmap/archive/10 Task T4) — a
			// non-integral delta here means a processor built a fractional
			// entry amount, which IntegralAmountValidator and
			// validateBalanced should already have caught upstream.
			return nil, fmt.Errorf("apply system delta: internal invariant violated: non-integral delta for account %s: %s", id, delta)
		}

		var newBalance int64
		err := tx.QueryRowContext(ctx, `
			UPDATE account_balances
			SET    balance    = balance + $1::bigint,
			       updated_at = now()
			WHERE  account_id = $2
			RETURNING balance`,
			delta.IntPart(), id,
		).Scan(&newBalance)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", apperror.ErrAccountNotFound, id)
		}
		if err != nil {
			return nil, fmt.Errorf("apply system delta for %s: %w", id, err)
		}
		result[id] = decimal.NewFromInt(newBalance)
	}

	return result, nil
}

func (r *balanceRepo) UpdateBalances(
	ctx context.Context,
	tx *sql.Tx,
	newBalances map[uuid.UUID]decimal.Decimal,
) error {
	if len(newBalances) == 0 {
		return nil
	}

	ids := generalutil.SortedDecimalKeys(newBalances)
	n := len(ids)

	// [FIX 2026-07-11, docs/roadmap/archive/10 Task T4] Reject non-integral balances
	// instead of silently truncating via IntPart(). By the time execution
	// reaches here, processors.IntegralAmountValidator and transport's
	// decimalFromString should already have rejected any fractional amount
	// — this is the last-resort safety net. A silent IntPart() truncation
	// here would create or destroy money without any error surfacing
	// anywhere: balance_after in ledger_entries (computed from the same
	// decimal.Decimal in applyEntries) would then permanently disagree
	// with the truncated value actually persisted to account_balances,
	// and fn_verify_account_balance would only catch it on the next
	// scheduled run — hours after the damage was already committed.
	for _, id := range ids {
		bal := newBalances[id]
		if !bal.Equal(bal.Truncate(0)) {
			return fmt.Errorf("update balances: internal invariant violated: non-integral balance for account %s: %s", id, bal)
		}
	}

	// args layout: [id_0, id_1, ..., id_{n-1}, bal_0, bal_1, ..., bal_{n-1}]
	// balance column is BIGINT (decision D2, minor units) — bind IntPart()
	// explicitly rather than decimal.Decimal itself (whose driver.Valuer
	// emits a string). Safe now that the loop above has confirmed every
	// balance is already integral — IntPart() cannot silently drop
	// anything from this point on.
	args := make([]any, 0, n*2)
	for _, id := range ids {
		args = append(args, id)
	}
	for _, id := range ids {
		args = append(args, newBalances[id].IntPart())
	}

	// [FIX 2026-07-11] Each THEN placeholder is cast to ::bigint explicitly.
	// With multiple WHEN/THEN branches, PostgreSQL's prepared-statement
	// parameter type inference does not propagate the target column's type
	// into each placeholder individually — it falls back to resolving them
	// as text, which then fails two different ways: the server rejects the
	// untyped CASE result against the bigint column ("column \"balance\" is
	// of type bigint but expression is of type text", SQLSTATE 42804), and
	// even after casting only the CASE's overall result, pgx still fails
	// client-side trying to encode an int64 using the text-typed encode
	// plan it was told to use for that placeholder ("unable to encode ...
	// into text format for text (OID 25): cannot find encode plan"). Only
	// casting each placeholder inline fixes both — a bare `col = $1`
	// assignment resolves fine without a cast, but a CASE wrapping multiple
	// placeholders does not. Only surfaced once run against real Postgres —
	// sqlmock doesn't validate SQL semantics.
	cases := make([]string, n)
	inParts := make([]string, n)
	for i := range ids {
		cases[i] = fmt.Sprintf("WHEN account_id=$%d THEN $%d::bigint", i+1, n+i+1)
		inParts[i] = fmt.Sprintf("$%d", i+1)
	}

	// No optimistic-locking version bump needed: LockBalances already took a
	// FOR UPDATE row lock on these accounts earlier in the same transaction.
	q := fmt.Sprintf(`
		UPDATE account_balances
		SET    balance    = CASE %s END,
		       updated_at = now()
		WHERE  account_id IN (%s)`,
		strings.Join(cases, " "), strings.Join(inParts, ","))

	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("update balances: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected != int64(n) {
		return fmt.Errorf("balance update: expected %d rows, got %d", n, affected)
	}
	return nil
}

func (r *balanceRepo) GetBalance(ctx context.Context, accountID uuid.UUID) (model.AccountBalance, error) {
	var ab model.AccountBalance
	err := r.db.QueryRowContext(ctx, `
		SELECT ab.account_id, ab.balance, a.currency, a.status, a.type, ab.allow_negative
		FROM   account_balances ab
		JOIN   accounts a ON a.id = ab.account_id
		WHERE  ab.account_id = $1`,
		accountID,
	).Scan(&ab.AccountID, &ab.Balance, &ab.Currency, &ab.Status, &ab.Type, &ab.AllowNegative)
	if errors.Is(err, sql.ErrNoRows) {
		return model.AccountBalance{}, fmt.Errorf("%w: %s", apperror.ErrAccountNotFound, accountID)
	}
	if err != nil {
		return model.AccountBalance{}, fmt.Errorf("get balance: %w", err)
	}
	return ab, nil
}
