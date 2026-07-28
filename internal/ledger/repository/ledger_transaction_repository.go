package repository

//go:generate mockgen -source=ledger_transaction_repository.go -destination=ledger_transaction_repository_mock.go -package=repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/cryptox"
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

	// ExternalRef/Gateway (docs/roadmap/archive/16 Task T2, K5) and RequestID
	// (docs/roadmap/archive/36 Task T5) are purely informative correlation columns —
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

	// FindConflictOrDuplicate is docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T3's (K7)
	// digest-first replacement for the old GetStatusByIdempotency: called
	// only after Insert has already hit a unique-key violation for
	// (key, scope), so a matching row is known to exist — this just needs
	// to find it and decide whether it's a legitimate retry or a
	// different request colliding on the same key.
	//
	// Lookup is digest-first (computed fresh from key/scope under the
	// ring's CURRENT version) with a raw-key/scope fallback — the fallback
	// is what makes "preserving temporary raw compatibility" (T3 work item
	// 4) real: during an active key rotation, a row backfilled to the new
	// digest version hasn't happened yet for every existing row, so a
	// fresh current-version digest can legitimately fail to match an old
	// row's still-old-version digest even though the raw (key, scope) is
	// identical. Once raw is nulled by retention (30+ days later), the
	// fallback simply never matches anything, which is correct — by then
	// backfill has long since caught every row up to a current-version
	// digest.
	//
	// status=="" means the row that caused the caller's unique-violation
	// vanished (a genuine race, not a normal miss) — same contract
	// GetStatusByIdempotency used to document. conflict==true means a row
	// WAS found but its stored conflict_fingerprint (type, amount,
	// currency) does not match the caller's own — a different request
	// reusing the same idempotency key, not a legitimate retry.
	FindConflictOrDuplicate(
		ctx context.Context,
		tx *sql.Tx,
		key string,
		scope *string,
		txType string,
		amount decimal.Decimal,
		currency string,
	) (status string, conflict bool, err error)

	// BackfillOnce is docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T3's bounded backfill
	// (work item 3): one batch of pre-T3 rows (idempotency_key_digest IS
	// NULL) gets a digest + conflict_fingerprint computed from their
	// still-present raw key/scope/type/amount/currency and written in
	// place — same shape as every T2.5 repository's own BackfillOnce
	// (WHERE-IS-NULL-driven, FOR UPDATE SKIP LOCKED, caller loops until 0
	// is itself the completion proof).
	BackfillOnce(ctx context.Context, batchSize int) (int, error)

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

	// ListByAccountEitherSide returns transactions where accountID is
	// EITHER the source or the destination, newest first, paginated by
	// (created_at, id) cursor — same cursor shape as EntryRepository's own
	// ListByAccount (Plan 57 T5's tenant-scoped transaction reads).
	// Read-only, outside any posting transaction. Backed by the existing
	// idx_ltx_src/idx_ltx_dest indexes (migrations/ledger/000001).
	ListByAccountEitherSide(
		ctx context.Context,
		accountID uuid.UUID,
		beforeCreatedAt time.Time,
		beforeID uuid.UUID,
		limit int,
	) ([]model.LedgerTransaction, error)

	// GetByIdempotencyKey looks up the posted transaction for a known,
	// deterministic idempotency key — used by the maker-checker adjustment
	// flow (docs/roadmap/archive/16 Task T1) to recover the transaction id after
	// Post() succeeds (Post itself only returns error, not the id it
	// created). Read-only, outside any posting transaction.
	GetByIdempotencyKey(ctx context.Context, key string, scope *string) (model.LedgerTransaction, error)

	// GetHeader returns type/status/amount/closed_by_tx_id for a
	// transaction, read within the caller's posting transaction — used by
	// lifecycle processors (docs/roadmap/archive/14 Task T2) to validate an original
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
	// original (docs/roadmap/archive/14 Task T2, decision K3). reason='reversed' also
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
	db         database.DatabaseSQL
	digestRing *cryptox.DigestRing
}

// NewTransactionRepository requires a DB handle (outside any ledger
// transaction) for read-only lookups such as GetAccountIDs, and a
// docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T3 (K7) digest ring — unlike every T2
// repository's OPTIONAL nil-safe ring (privacy fields degrade gracefully
// without one), digestRing is REQUIRED here: idempotency deduplication is
// a money-safety invariant, not a confidentiality one, and K7 is explicit
// that "a missing key version... never bypasses deduplication." A nil
// ring panics immediately rather than letting posting silently run
// without ever computing a digest.
func NewTransactionRepository(db database.DatabaseSQL, digestRing *cryptox.DigestRing) TransactionRepository {
	if digestRing == nil {
		panic("ledger: digest ring is required (docs/roadmap/archive/51-a8-data-lifecycle-privacy.md K7)")
	}
	return &transactionRepo{db: db, digestRing: digestRing}
}

// canonicalIdempotencyInput length-prefixes scope and key before
// concatenating them — a plain "scope+key" string would let
// scope="ab",key="c" collide with scope="a",key="bc", silently merging two
// distinct idempotency identities into the same digest.
func canonicalIdempotencyInput(scope *string, key string) []byte {
	s := ""
	if scope != nil {
		s = *scope
	}
	buf := make([]byte, 0, 8+len(s)+len(key))
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(s)))
	buf = append(buf, lenBuf[:]...)
	buf = append(buf, s...)
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(key)))
	buf = append(buf, lenBuf[:]...)
	buf = append(buf, key...)
	return buf
}

// conflictFingerprint hashes the business-conflict-relevant fields of a
// posting attempt — plain SHA-256 (unlike the idempotency digest, this
// never needs to resist offline guessing; its only job is exact-match
// comparison against itself), same length-prefixing discipline as
// canonicalIdempotencyInput.
func conflictFingerprint(txType string, amount decimal.Decimal, currency string) []byte {
	h := sha256.New()
	for _, part := range []string{txType, amount.String(), currency} {
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(part)))
		h.Write(lenBuf[:])
		h.Write([]byte(part))
	}
	return h.Sum(nil)
}

func (r *transactionRepo) Insert(
	ctx context.Context,
	tx *sql.Tx,
	p InsertTransactionParams,
) error {
	digest, version := r.digestRing.Digest(canonicalIdempotencyInput(p.IdempotencyScope, p.IdempotencyKey))
	fingerprint := conflictFingerprint(p.Type, p.Amount, p.Currency)

	_, err := tx.ExecContext(ctx, `
		INSERT INTO ledger_transactions
			(id, idempotency_key, idempotency_scope, type, status,
			 amount, currency, source_account_id, destination_account_id,
			 external_ref, gateway, request_id, created_at, updated_at,
			 idempotency_key_digest, idempotency_key_version, conflict_fingerprint)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now(),now(),$13,$14,$15)`,
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
		digest,
		version,
		fingerprint,
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

func (r *transactionRepo) FindConflictOrDuplicate(
	ctx context.Context,
	tx *sql.Tx,
	key string,
	scope *string,
	txType string,
	amount decimal.Decimal,
	currency string,
) (string, bool, error) {
	digest, _ := r.digestRing.Digest(canonicalIdempotencyInput(scope, key))

	var status string
	var storedFingerprint []byte
	err := tx.QueryRowContext(ctx, `
		SELECT status, conflict_fingerprint
		FROM ledger_transactions
		WHERE idempotency_key_digest = $1
		LIMIT 1`,
		digest,
	).Scan(&status, &storedFingerprint)

	if errors.Is(err, sql.ErrNoRows) {
		// Rotation-transition fallback (see this method's own interface
		// doc comment) — only ever matches a row whose raw key/scope is
		// still present (pre-30-day-retention, or not yet nulled).
		err = tx.QueryRowContext(ctx, `
			SELECT status, conflict_fingerprint
			FROM ledger_transactions
			WHERE idempotency_key = $1
			  AND (idempotency_scope = $2 OR ($2 IS NULL AND idempotency_scope IS NULL))
			LIMIT 1`,
			key, scope,
		).Scan(&status, &storedFingerprint)
	}

	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	conflict := len(storedFingerprint) > 0 && !bytes.Equal(storedFingerprint, conflictFingerprint(txType, amount, currency))
	return status, conflict, nil
}

func (r *transactionRepo) BackfillOnce(ctx context.Context, batchSize int) (int, error) {
	type pendingRow struct {
		id       uuid.UUID
		key      string
		scope    *string
		txType   string
		amount   decimal.Decimal
		currency string
	}
	var rows []pendingRow
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		result, queryErr := tx.QueryContext(ctx, `
			SELECT id, idempotency_key, idempotency_scope, type, amount, currency
			FROM ledger_transactions
			WHERE idempotency_key_digest IS NULL
			ORDER BY created_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED`, batchSize)
		if queryErr != nil {
			return fmt.Errorf("select backfill batch: %w", queryErr)
		}
		for result.Next() {
			var pr pendingRow
			var scope sql.NullString
			var key sql.NullString
			if scanErr := result.Scan(&pr.id, &key, &scope, &pr.txType, &pr.amount, &pr.currency); scanErr != nil {
				result.Close()
				return fmt.Errorf("scan backfill row: %w", scanErr)
			}
			if !key.Valid {
				// idempotency_key is already nulled (retention ran before
				// backfill reached this row) — nothing left to derive a
				// digest from; skip rather than write a digest over an
				// empty key that could collide with a real one.
				continue
			}
			pr.key = key.String
			if scope.Valid {
				s := scope.String
				pr.scope = &s
			}
			rows = append(rows, pr)
		}
		if rowsErr := result.Err(); rowsErr != nil {
			result.Close()
			return fmt.Errorf("iterate backfill batch: %w", rowsErr)
		}
		result.Close()

		for _, pr := range rows {
			digest, version := r.digestRing.Digest(canonicalIdempotencyInput(pr.scope, pr.key))
			fingerprint := conflictFingerprint(pr.txType, pr.amount, pr.currency)
			if _, execErr := tx.ExecContext(ctx, `
				UPDATE ledger_transactions
				SET idempotency_key_digest = $1, idempotency_key_version = $2, conflict_fingerprint = $3
				WHERE id = $4`,
				digest, version, fingerprint, pr.id); execErr != nil {
				return fmt.Errorf("update backfilled transaction %s: %w", pr.id, execErr)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(rows), nil
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

func (r *transactionRepo) ListByAccountEitherSide(
	ctx context.Context,
	accountID uuid.UUID,
	beforeCreatedAt time.Time,
	beforeID uuid.UUID,
	limit int,
) ([]model.LedgerTransaction, error) {
	var rows *sql.Rows
	var err error
	// [same first-page convention as EntryRepository.ListByAccount] a zero
	// beforeCreatedAt means "start from the most recent transaction" — an
	// unconditional (created_at, id) < (zero-time, ...) filter would match
	// no real row at all, silently returning an empty first page.
	if beforeCreatedAt.IsZero() {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, idempotency_key, idempotency_scope, type, status, amount, currency,
			       source_account_id, destination_account_id, error_message,
			       external_ref, gateway, created_at, updated_at
			FROM ledger_transactions
			WHERE source_account_id = $1 OR destination_account_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2`,
			accountID, limit,
		)
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, idempotency_key, idempotency_scope, type, status, amount, currency,
			       source_account_id, destination_account_id, error_message,
			       external_ref, gateway, created_at, updated_at
			FROM ledger_transactions
			WHERE (source_account_id = $1 OR destination_account_id = $1)
			  AND (created_at, id) < ($2, $3)
			ORDER BY created_at DESC, id DESC
			LIMIT $4`,
			accountID, beforeCreatedAt, beforeID, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list transactions by account: %w", err)
	}
	defer rows.Close()

	txs := make([]model.LedgerTransaction, 0)
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		txs = append(txs, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transactions: %w", err)
	}
	return txs, nil
}

// sqlScanner is implemented identically by *sql.Row and *sql.Rows, letting
// scanTransaction serve both GetByID/GetByIdempotencyKey (single row) and
// ListByAccountEitherSide (many rows) without duplicating the column list.
type sqlScanner interface {
	Scan(dest ...any) error
}

// scanTransaction is the shared row-scanning logic for GetByID,
// GetByIdempotencyKey, and ListByAccountEitherSide — same columns, same
// NULL/UUID handling, different WHERE clause.
func scanTransaction(row sqlScanner) (model.LedgerTransaction, error) {
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
	// [docs/roadmap/archive/12 Task T6] uuid.Parse with error handling, not
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
