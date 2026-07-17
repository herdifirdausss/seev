package repository

//go:generate mockgen -source=ledger_transaction_repository.go -destination=ledger_transaction_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/shopspring/decimal"
)

type InsertTransactionParams struct {
	ID uuid.UUID

	IdempotencyKey   string
	IdempotencyScope *string

	Type string

	Amount   decimal.Decimal
	Currency string

	SourceAccountID      *uuid.UUID
	DestinationAccountID *uuid.UUID

	// ExternalRef/Gateway (docs/plan/16 Task T2, K5) and RequestID
	// (docs/plan/36 Task T5) are purely informative correlation columns —
	// absent for the large majority of transaction types that never carry
	// this metadata (transfer_p2p, adjustment_*, etc.).
	ExternalRef *string
	Gateway     *string
	RequestID   *string
}

// TransactionRepository abstracts persistence operations for ledger_transactions.
// The repository does not encode domain semantics (posted, reversed, failed, etc).
// Business logic determines status transitions.
//
// All methods receive a *sql.Tx so they run within the same DB transaction.
type TransactionRepository interface {

	// Insert creates a new transaction record.
	// Typically called when a processor begins execution.
	Insert(ctx context.Context, tx *sql.Tx, params InsertTransactionParams) error

	// GetStatus returns the current status of a transaction, read within
	// the caller's posting transaction (e.g. Reversal.Validate).
	GetStatus(ctx context.Context, tx *sql.Tx, transactionID uuid.UUID) (string, error)

	// UpdateStatus updates the status of a transaction.
	// Domain logic determines the status value (posted, failed, reversed, etc).
	UpdateStatus(
		ctx context.Context,
		tx *sql.Tx,
		transactionID uuid.UUID,
		status string,
		errorMessage *string,
	) error

	// GetStatusByIdempotency returns the status of a transaction
	// identified by idempotency key and scope.
	GetStatusByIdempotency(
		ctx context.Context,
		tx *sql.Tx,
		key string,
		scope *string,
	) (string, error)

	// GetEntries returns all ledger entries associated with a transaction.
	// Used by processors such as reversal to reconstruct accounting movements.
	GetEntries(
		ctx context.Context,
		tx *sql.Tx,
		transactionID uuid.UUID,
	) ([]model.LedgerEntryRecord, error)

	// GetAccountIDs returns the distinct accounts associated with the
	// transaction. Read-only lookup used by processors (e.g.
	// Reversal.ResolveAccounts) to determine which accounts must be locked
	// — called BEFORE the posting transaction begins, so no *sql.Tx exists
	// yet at that point.
	GetAccountIDs(
		ctx context.Context,
		transactionID uuid.UUID,
	) ([]uuid.UUID, error)

	// GetByID returns the full transaction header for read APIs (GET
	// /transactions/{id}). Read-only lookup outside any posting transaction.
	GetByID(ctx context.Context, transactionID uuid.UUID) (model.LedgerTransaction, error)

	// GetByIdempotencyKey looks up the posted transaction for a known,
	// deterministic idempotency key — used by the maker-checker adjustment
	// flow (docs/plan/16 Task T1) to recover the transaction id after
	// Post() succeeds (Post itself only returns error, not the id it
	// created). Read-only, outside any posting transaction.
	GetByIdempotencyKey(ctx context.Context, key string, scope *string) (model.LedgerTransaction, error)

	// GetHeader returns type/status/amount/closed_by_tx_id for a
	// transaction, read within the caller's posting transaction — used by
	// lifecycle processors (docs/plan/14 Task T2) to validate an original
	// transaction (right type, posted, not already closed, matching amount)
	// before attempting to close it. This is a fast-fail convenience check;
	// CloseOriginal's atomic UPDATE is the actual race-proof guard.
	GetHeader(
		ctx context.Context,
		tx *sql.Tx,
		transactionID uuid.UUID,
	) (txType, status string, amount decimal.Decimal, closedByTxID *uuid.UUID, err error)

	// CloseOriginal atomically marks originalID as closed by byTxID for the
	// given reason ('reversed'|'settled'|'cancelled'|'released'|'refunded')
	// — a single UPDATE guarded by `WHERE closed_by_tx_id IS NULL` that only
	// succeeds once, no matter how many concurrent closers race for the same
	// original (docs/plan/14 Task T2, decision K3). reason='reversed' also
	// sets status='reversed' in the same statement, preserving the existing
	// GetStatus contract. Returns rows affected: 1 on success, 0 if
	// originalID was already closed — callers must treat 0 as a business
	// failure (apperror.ErrAlreadyClosed), not silently proceed.
	CloseOriginal(
		ctx context.Context,
		tx *sql.Tx,
		originalID, byTxID uuid.UUID,
		reason string,
	) (int64, error)
}

type transactionRepo struct {
	db database.DatabaseSQL
}

// NewTransactionRepository requires a DB handle (outside any ledger
// transaction) for read-only lookups such as GetAccountIDs.
func NewTransactionRepository(db database.DatabaseSQL) TransactionRepository {
	return &transactionRepo{db: db}
}

func (r *transactionRepo) Insert(
	ctx context.Context,
	tx *sql.Tx,
	p InsertTransactionParams,
) error {

	_, err := tx.ExecContext(ctx, `
		INSERT INTO ledger_transactions
			(id, idempotency_key, idempotency_scope, type, status,
			 amount, currency, source_account_id, destination_account_id,
			 external_ref, gateway, request_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now(),now())`,
		p.ID,
		p.IdempotencyKey,
		p.IdempotencyScope,
		p.Type,
		"pending",
		p.Amount,
		p.Currency,
		p.SourceAccountID,
		p.DestinationAccountID,
		p.ExternalRef,
		p.Gateway,
		p.RequestID,
	)

	if err != nil {
		return err
	}

	return nil
}

func (r *transactionRepo) UpdateStatus(
	ctx context.Context,
	tx *sql.Tx,
	id uuid.UUID,
	status string,
	errorMessage *string,
) error {

	_, err := tx.ExecContext(ctx, `
		UPDATE ledger_transactions
		SET
			status = $1,
			error_message = $2,
			updated_at = now()
		WHERE id = $3`,
		status,
		errorMessage,
		id,
	)

	return err
}

func (r *transactionRepo) GetStatus(
	ctx context.Context,
	tx *sql.Tx,
	transactionID uuid.UUID,
) (string, error) {

	var status string

	err := tx.QueryRowContext(ctx, `
		SELECT status
		FROM ledger_transactions
		WHERE id = $1`,
		transactionID,
	).Scan(&status)

	if err != nil {
		return "", err
	}

	return status, nil
}

func (r *transactionRepo) GetStatusByIdempotency(
	ctx context.Context,
	tx *sql.Tx,
	key string,
	scope *string,
) (string, error) {

	var status string

	err := tx.QueryRowContext(ctx, `
		SELECT status
		FROM ledger_transactions
		WHERE idempotency_key = $1
		  AND (idempotency_scope = $2 OR ($2 IS NULL AND idempotency_scope IS NULL))
		LIMIT 1`,
		key,
		scope,
	).Scan(&status)

	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}

	return status, nil
}

func (r *transactionRepo) GetEntries(
	ctx context.Context,
	tx *sql.Tx,
	transactionID uuid.UUID,
) ([]model.LedgerEntryRecord, error) {

	rows, err := tx.QueryContext(ctx, `
		SELECT
			id,
			account_id,
			direction,
			amount
		FROM ledger_entries
		WHERE transaction_id = $1
		ORDER BY id
	`, transactionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []model.LedgerEntryRecord

	for rows.Next() {
		var entry model.LedgerEntryRecord

		err := rows.Scan(
			&entry.EntryID,
			&entry.AccountID,
			&entry.Direction,
			&entry.Amount,
		)
		if err != nil {
			return nil, err
		}

		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return entries, nil
}

func (r *transactionRepo) GetAccountIDs(
	ctx context.Context,
	transactionID uuid.UUID,
) ([]uuid.UUID, error) {

	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT account_id
		FROM ledger_entries
		WHERE transaction_id = $1
	`, transactionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID

	for rows.Next() {
		var id uuid.UUID

		if err := rows.Scan(&id); err != nil {
			return nil, err
		}

		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return ids, nil
}

func (r *transactionRepo) GetByID(ctx context.Context, transactionID uuid.UUID) (model.LedgerTransaction, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, idempotency_key, idempotency_scope, type, status, amount, currency,
		       source_account_id, destination_account_id, error_message,
		       external_ref, gateway, created_at, updated_at
		FROM ledger_transactions
		WHERE id = $1`,
		transactionID,
	)
	t, err := scanTransaction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.LedgerTransaction{}, fmt.Errorf("%w: transaction %s", apperror.ErrTransactionNotFound, transactionID)
	}
	return t, err
}

func (r *transactionRepo) GetByIdempotencyKey(ctx context.Context, key string, scope *string) (model.LedgerTransaction, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, idempotency_key, idempotency_scope, type, status, amount, currency,
		       source_account_id, destination_account_id, error_message,
		       external_ref, gateway, created_at, updated_at
		FROM ledger_transactions
		WHERE idempotency_key = $1
		  AND (idempotency_scope = $2 OR ($2 IS NULL AND idempotency_scope IS NULL))`,
		key, scope,
	)
	t, err := scanTransaction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.LedgerTransaction{}, fmt.Errorf("%w: idempotency_key %q", apperror.ErrTransactionNotFound, key)
	}
	return t, err
}

// scanTransaction is the shared row-scanning logic for GetByID and
// GetByIdempotencyKey — same columns, same NULL/UUID handling, different
// WHERE clause.
func scanTransaction(row *sql.Row) (model.LedgerTransaction, error) {
	var (
		t                model.LedgerTransaction
		idempotencyScope sql.NullString
		sourceAccountID  sql.NullString
		destAccountID    sql.NullString
		errorMessage     sql.NullString
		externalRef      sql.NullString
		gateway          sql.NullString
	)

	err := row.Scan(
		&t.ID, &t.IdempotencyKey, &idempotencyScope, &t.Type, &t.Status, &t.Amount, &t.Currency,
		&sourceAccountID, &destAccountID, &errorMessage,
		&externalRef, &gateway, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.LedgerTransaction{}, err
		}
		return model.LedgerTransaction{}, fmt.Errorf("get transaction: %w", err)
	}

	t.IdempotencyScope = idempotencyScope.String
	t.ErrorMessage = errorMessage.String
	t.ExternalRef = externalRef.String
	t.Gateway = gateway.String
	// [docs/plan/12 Task T6] uuid.Parse with error handling, not
	// uuid.MustParse — a single malformed stored UUID (data corruption,
	// manual DB intervention gone wrong) must return an error to the
	// caller, not panic the whole process over one bad row.
	if sourceAccountID.Valid {
		id, err := uuid.Parse(sourceAccountID.String)
		if err != nil {
			return model.LedgerTransaction{}, fmt.Errorf("scan transaction: invalid stored source_account_id: %w", err)
		}
		t.SourceAccountID = id
	}
	if destAccountID.Valid {
		id, err := uuid.Parse(destAccountID.String)
		if err != nil {
			return model.LedgerTransaction{}, fmt.Errorf("scan transaction: invalid stored destination_account_id: %w", err)
		}
		t.DestinationAccountID = id
	}
	return t, nil
}

func (r *transactionRepo) GetHeader(
	ctx context.Context,
	tx *sql.Tx,
	transactionID uuid.UUID,
) (string, string, decimal.Decimal, *uuid.UUID, error) {
	var (
		txType, status string
		amount         decimal.Decimal
		closedBy       sql.NullString
	)

	err := tx.QueryRowContext(ctx, `
		SELECT type, status, amount, closed_by_tx_id
		FROM ledger_transactions
		WHERE id = $1`,
		transactionID,
	).Scan(&txType, &status, &amount, &closedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", decimal.Zero, nil, fmt.Errorf("%w: %s", apperror.ErrOriginalNotFound, transactionID)
	}
	if err != nil {
		return "", "", decimal.Zero, nil, fmt.Errorf("get header: %w", err)
	}

	var closedByTxID *uuid.UUID
	if closedBy.Valid {
		id, err := uuid.Parse(closedBy.String)
		if err != nil {
			return "", "", decimal.Zero, nil, fmt.Errorf("get header: invalid stored closed_by_tx_id: %w", err)
		}
		closedByTxID = &id
	}

	return txType, status, amount, closedByTxID, nil
}

func (r *transactionRepo) CloseOriginal(
	ctx context.Context,
	tx *sql.Tx,
	originalID, byTxID uuid.UUID,
	reason string,
) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE ledger_transactions
		SET closed_by_tx_id = $1,
		    closed_reason = $2,
		    status = CASE WHEN $2 = 'reversed' THEN 'reversed' ELSE status END,
		    updated_at = now()
		WHERE id = $3
		  AND closed_by_tx_id IS NULL`,
		byTxID, reason, originalID,
	)
	if err != nil {
		return 0, fmt.Errorf("close original: %w", err)
	}
	return res.RowsAffected()
}
