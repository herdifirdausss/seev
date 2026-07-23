package drverify

import (
	"context"
	"database/sql"
	"fmt"
)

// checkLedgerBalance wraps fn_verify_ledger_balance('-infinity','infinity')
// — the exact function docs/runbooks/dr-restore-drill.md's manual gate
// already calls, over the FULL history rather than a bounded window,
// since a restore drill has no "already checked yesterday" assumption to
// lean on. Any row returned means a transaction's debit and credit
// entries don't sum equal — K9's "unbalanced ledger transaction", always
// fatal.
func checkLedgerBalance(ctx context.Context, db *sql.DB, cfg Config, report *Report) error {
	return readOnlyQuery(ctx, db, cfg, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT transaction_id, sum_debit, sum_credit, diff FROM fn_verify_ledger_balance('-infinity'::timestamptz, 'infinity'::timestamptz)`)
		if err != nil {
			return fmt.Errorf("fn_verify_ledger_balance: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var txID string
			var sumDebit, sumCredit, diff int64
			if err := rows.Scan(&txID, &sumDebit, &sumCredit, &diff); err != nil {
				return fmt.Errorf("scan unbalanced transaction: %w", err)
			}
			report.addFinding(Finding{
				Code: "LEDGER_UNBALANCED_TRANSACTION", Severity: SeverityFatal, Service: "ledger",
				ResourceID: txID,
				Message:    "transaction's debit and credit entries do not sum equal",
				Evidence: map[string]string{
					"sum_debit": fmt.Sprint(sumDebit), "sum_credit": fmt.Sprint(sumCredit), "diff": fmt.Sprint(diff),
				},
			})
		}
		return rows.Err()
	})
}

// checkProjection is an UNWINDOWED equivalent of v_account_balance_audit
// — that view only covers accounts touched in the last 24h (a live-
// service monitoring choice), which is the wrong scope for a one-shot
// restore-drill check against the account's entire history. Computes
// each account's balance directly from ledger_entries (source of truth)
// and compares against the account_balances projection.
func checkProjection(ctx context.Context, db *sql.DB, cfg Config, report *Report) error {
	const query = `
		SELECT a.id, ab.balance, COALESCE(SUM(CASE WHEN e.direction = 'credit' THEN e.amount ELSE -e.amount END), 0) AS computed
		FROM accounts a
		JOIN account_balances ab ON ab.account_id = a.id
		LEFT JOIN ledger_entries e ON e.account_id = a.id
		GROUP BY a.id, ab.balance
		HAVING ab.balance <> COALESCE(SUM(CASE WHEN e.direction = 'credit' THEN e.amount ELSE -e.amount END), 0)`
	return readOnlyQuery(ctx, db, cfg, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("projection consistency query: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var accountID string
			var stored, computed int64
			if err := rows.Scan(&accountID, &stored, &computed); err != nil {
				return fmt.Errorf("scan projection mismatch: %w", err)
			}
			report.addFinding(Finding{
				Code: "LEDGER_PROJECTION_INCONSISTENT", Severity: SeverityFatal, Service: "ledger",
				ResourceID: accountID,
				Message:    "account_balances projection disagrees with ledger_entries",
				Evidence:   map[string]string{"stored_balance": fmt.Sprint(stored), "computed_balance": fmt.Sprint(computed)},
			})
		}
		return rows.Err()
	})
}
