package repository

//go:generate mockgen -source=ledger_entry_repository.go -destination=ledger_entry_repository_mock.go -package=repository
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/database/identifiers"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/constants"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/shopspring/decimal"
)

// maxEntriesBatch caps a single InsertEntries call — a real posting never
// produces more than a handful of entries (2-3 typical, more for a
// multi-leg reversal), so this is a safety ceiling, not a normal operating
// limit (docs/roadmap/archive/11 Task T2, mirrors outbox_event_repository.go's
// maxOutboxBatch).
const maxEntriesBatch = 50

type EntryRepository interface {
	InsertEntries(
		ctx context.Context,
		tx *sql.Tx,
		txID uuid.UUID,
		entries []model.EntryInstruction,
		newBalances map[uuid.UUID]decimal.Decimal,
	) error

	// ListByAccount returns entries for an account, newest first, using
	// keyset pagination. Pass a zero time.Time and uuid.Nil for the first
	// page; for subsequent pages pass the (CreatedAt, ID) of the last entry
	// from the previous page.
	ListByAccount(
		ctx context.Context,
		accountID uuid.UUID,
		beforeCreatedAt time.Time,
		beforeID uuid.UUID,
		limit int,
	) ([]model.LedgerEntry, error)

	// ListByAccountRange returns entries for accountID within [from, to]
	// (Asia/Jakarta calendar days, both inclusive), oldest first — a
	// statement reads top-to-bottom chronologically, unlike ListByAccount's
	// newest-first pagination (docs/roadmap/archive/15 Task T2). Joined with the
	// owning transaction's type. Callers pass limit = maxRows+1 to detect
	// "too many rows in range" without a separate COUNT query.
	ListByAccountRange(
		ctx context.Context,
		accountID uuid.UUID,
		from, to time.Time,
		loc *time.Location,
		limit int,
	) ([]model.StatementEntry, error)
}

type entryRepo struct {
	db database.DatabaseSQL
}

// NewEntryRepository requires a DB handle for ListByAccount's read-only
// lookups; InsertEntries always runs against the tx passed in.
func NewEntryRepository(db database.DatabaseSQL) EntryRepository {
	return &entryRepo{db: db}
}

// InsertEntries crosses the database posting boundary in one round trip. The
// app role has no direct INSERT privilege on ledger_entries; migration 000049's
// SECURITY DEFINER function validates the complete batch and inserts it in the
// caller's existing transaction. Immutable-entry and deferred balance triggers
// still run normally on the rows written by the function.
func (r *entryRepo) InsertEntries(
	ctx context.Context,
	tx *sql.Tx,
	txID uuid.UUID,
	entries []model.EntryInstruction,
	newBalances map[uuid.UUID]decimal.Decimal,
) error {
	if len(entries) == 0 {
		return nil
	}
	if len(entries) > maxEntriesBatch {
		return fmt.Errorf("entries batch too large: %d > %d", len(entries), maxEntriesBatch)
	}

	type postingEntry struct {
		ID           uuid.UUID `json:"id"`
		AccountID    uuid.UUID `json:"account_id"`
		Direction    string    `json:"direction"`
		Amount       string    `json:"amount"`
		BalanceAfter string    `json:"balance_after"`
		Note         string    `json:"note,omitempty"`
	}
	payload := make([]postingEntry, 0, len(entries))
	for _, entry := range entries {
		payload = append(payload, postingEntry{
			ID:           identifiers.NewV7(),
			AccountID:    entry.AccountID,
			Direction:    string(entry.Direction),
			Amount:       entry.Amount.String(),
			BalanceAfter: newBalances[entry.AccountID].String(),
			Note:         entry.Note,
		})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode ledger posting entries: %w", err)
	}
	var inserted int
	if err := tx.QueryRowContext(ctx,
		`SELECT public.fn_post_ledger_entries_bulk($1, $2::jsonb)`, txID, string(encoded),
	).Scan(&inserted); err != nil {
		return fmt.Errorf("post ledger entries: %w", err)
	}
	if inserted != len(entries) {
		return fmt.Errorf("post ledger entries: inserted %d entries, expected %d", inserted, len(entries))
	}
	return nil
}

func (r *entryRepo) ListByAccount(
	ctx context.Context,
	accountID uuid.UUID,
	beforeCreatedAt time.Time,
	beforeID uuid.UUID,
	limit int,
) ([]model.LedgerEntry, error) {
	var rows *sql.Rows
	var err error

	if beforeCreatedAt.IsZero() {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, transaction_id, account_id, direction, amount, balance_after, note, created_at
			FROM ledger_entries
			WHERE account_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2`,
			accountID, limit,
		)
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, transaction_id, account_id, direction, amount, balance_after, note, created_at
			FROM ledger_entries
			WHERE account_id = $1 AND (created_at, id) < ($2, $3)
			ORDER BY created_at DESC, id DESC
			LIMIT $4`,
			accountID, beforeCreatedAt, beforeID, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()

	entries := make([]model.LedgerEntry, 0, limit)
	for rows.Next() {
		var e model.LedgerEntry
		var direction string
		var note sql.NullString
		if err := rows.Scan(&e.ID, &e.TransactionID, &e.AccountID, &direction,
			&e.Amount, &e.BalanceAfter, &note, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan entry: %w", err)
		}
		e.Direction = constant.Direction(direction)
		e.Note = note.String
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entries: %w", err)
	}
	return entries, nil
}

func (r *entryRepo) ListByAccountRange(
	ctx context.Context,
	accountID uuid.UUID,
	from, to time.Time,
	loc *time.Location,
	limit int,
) ([]model.StatementEntry, error) {
	if loc == nil {
		loc = time.UTC
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT le.id, le.transaction_id, lt.type, le.account_id, le.direction,
		       le.amount, le.balance_after, le.note, le.created_at
		FROM ledger_entries le
		JOIN ledger_transactions lt ON lt.id = le.transaction_id
		WHERE le.account_id = $1
		  AND le.created_at >= ($2::date::timestamp AT TIME ZONE $5)
		  AND le.created_at <  (($3::date + 1)::timestamp AT TIME ZONE $5)
		ORDER BY le.created_at ASC, le.id ASC
		LIMIT $4`,
		accountID, from.Format("2006-01-02"), to.Format("2006-01-02"), limit, loc.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("list entries by range: %w", err)
	}
	defer rows.Close()

	entries := make([]model.StatementEntry, 0, limit)
	for rows.Next() {
		var e model.StatementEntry
		var direction string
		var note sql.NullString
		if err := rows.Scan(&e.ID, &e.TransactionID, &e.TransactionType, &e.AccountID, &direction,
			&e.Amount, &e.BalanceAfter, &note, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan statement entry: %w", err)
		}
		e.Direction = constant.Direction(direction)
		e.Note = note.String
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate statement entries: %w", err)
	}
	return entries, nil
}
