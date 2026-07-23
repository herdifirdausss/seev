package drverify

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/herdifirdausss/seev/internal/assurance/rules"
)

// ledgerProofByCorrelation mirrors internal/ledger/assurance.go's
// assuranceLookup exactly (same WHERE clause, same closed_by_tx_id
// reverse-lookup for OriginalReferenceID, same booked-fee aggregate) —
// the one query shape internal/ledger's own gRPC handler uses to answer
// "what does the ledger say happened for type=X, gateway=Y,
// external_ref=Z", now run directly against the restored seev_ledger
// database instead of over gRPC.
func ledgerProofByCorrelation(ctx context.Context, tx *sql.Tx, txType, gateway, externalRef string) ([]rules.LedgerProof, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, type, status, amount, currency, COALESCE(gateway, ''), COALESCE(external_ref, ''), closed_by_tx_id, closed_reason, created_at, updated_at
		 FROM ledger_transactions WHERE type = $1 AND gateway = $2 AND external_ref = $3
		 ORDER BY created_at, id LIMIT 501`, txType, gateway, externalRef)
	if err != nil {
		return nil, fmt.Errorf("ledger correlation lookup: %w", err)
	}
	base, err := scanLedgerTransactions(rows)
	if err != nil {
		return nil, err
	}
	return enrichLedgerProofs(ctx, tx, base)
}

// ledgerProofByID mirrors the same handler's transaction_id selector
// path — used for payout hold/settle lookups, which are always by ID.
func ledgerProofByID(ctx context.Context, tx *sql.Tx, id string) (*rules.LedgerProof, error) {
	if id == "" {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT id, type, status, amount, currency, COALESCE(gateway, ''), COALESCE(external_ref, ''), closed_by_tx_id, closed_reason, created_at, updated_at
		 FROM ledger_transactions WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("ledger id lookup: %w", err)
	}
	base, err := scanLedgerTransactions(rows)
	if err != nil {
		return nil, err
	}
	proofs, err := enrichLedgerProofs(ctx, tx, base)
	if err != nil {
		return nil, err
	}
	if len(proofs) == 0 {
		return nil, nil
	}
	return &proofs[0], nil
}

type rawLedgerTransaction struct {
	id, txType, txStatus, currency, gateway, externalRef string
	amount                                               int64
	closedID, closedReason                               sql.NullString
}

// scanLedgerTransactions fully drains and closes rows before returning —
// deliberately not interleaved with the nested per-transaction queries
// enrichLedgerProofs runs afterward. A *sql.Tx is bound to one
// connection; running a second query on it while an earlier *sql.Rows
// from the SAME tx still has unread rows is invalid and was found live
// as a reproducible "driver: bad connection" error, not a transient
// flake — every one of dozens of identical runs against the same fixture
// failed identically until this was fixed.
func scanLedgerTransactions(rows *sql.Rows) ([]rawLedgerTransaction, error) {
	defer rows.Close()
	var out []rawLedgerTransaction
	for rows.Next() {
		var r rawLedgerTransaction
		var created, updated time.Time
		if err := rows.Scan(&r.id, &r.txType, &r.txStatus, &r.amount, &r.currency, &r.gateway, &r.externalRef, &r.closedID, &r.closedReason, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan ledger transaction: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// enrichLedgerProofs runs the booked-fee aggregate and reverse
// closed_by_tx_id lookup for each already-materialized transaction — safe
// to run sequentially on tx now that no other *sql.Rows from it is open.
func enrichLedgerProofs(ctx context.Context, tx *sql.Tx, base []rawLedgerTransaction) ([]rules.LedgerProof, error) {
	out := make([]rules.LedgerProof, 0, len(base))
	for _, r := range base {
		feeAmount, feeGateway, err := bookedFee(ctx, tx, r.id)
		if err != nil {
			return nil, err
		}
		proof := rules.LedgerProof{ID: r.id, Type: r.txType, Status: r.txStatus, AmountMinor: r.amount, Currency: r.currency, Gateway: r.gateway, ExternalRef: r.externalRef, BookedFeeMinor: feeAmount, BookedFeeGateway: feeGateway}
		if r.closedID.Valid {
			proof.LifecycleCloserID = r.closedID.String
		}
		var originalID sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT id::text FROM ledger_transactions WHERE closed_by_tx_id = $1`, r.id).Scan(&originalID); err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("reverse lifecycle lookup: %w", err)
		}
		proof.OriginalReferenceID = originalID.String
		out = append(out, proof)
	}
	return out, nil
}

// bookedFee mirrors internal/ledger/assurance.go's bookedFee exactly —
// sum of entries posted to a 'fee'-type account for this transaction,
// gateway taken from that account's system_qualifier.
func bookedFee(ctx context.Context, tx *sql.Tx, txID string) (int64, string, error) {
	var amount int64
	var gateway sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(e.amount), 0), MAX(COALESCE(a.system_qualifier, ''))
		FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
		WHERE e.transaction_id = $1 AND a.type = 'fee'`, txID).Scan(&amount, &gateway)
	if err != nil {
		return 0, "", fmt.Errorf("booked fee lookup: %w", err)
	}
	return amount, gateway.String, nil
}
