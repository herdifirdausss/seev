// Package closure implements ledger's own owner-side of docs/roadmap/active/51-a8-data-lifecycle-privacy.md
// T5's (K10, K11) account-closure/pseudonymization saga: "Ledger: account
// owner... references; no entry mutation." Deliberately does not touch
// ledger_entries/ledger_transactions at all — those stay byte-for-byte
// unchanged (K10's own explicit requirement); only accounts.owner_id, a
// mutable projection, moves to the surrogate.
package closure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/pkg/database"
)

type Service struct {
	db database.DatabaseSQL
}

func New(db database.DatabaseSQL) *Service {
	return &Service{db: db}
}

// Prepare checks K10's own ledger-owned blocking condition: "any non-zero
// cash, hold, pending, frozen, or pocket balance." Read-only, unlocked —
// a fast-fail UX check, not the enforcement point (Commit re-derives its
// own result from live data, so a balance that changes between Prepare
// and a later Commit attempt simply makes that Commit itself still no-op
// correctly on the accounts that remain owned, since only zero-balance
// accounts should exist by the time an operator actually proceeds to
// commit — see this package's own doc comment on why Commit does not
// re-verify zero balance itself: that enforcement lives entirely in
// Prepare/the caller's own gating, matching K10's "before pseudonymization,
// all owners must prepare successfully").
//
// Checks pending_adjustments (maker-checker requests) are NOT included: that
// table has no first-class user reference (only an opaque cmd_payload JSONB
// whose shape isn't guaranteed stable enough to match on reliably here) —
// documented as a known gap for A8 T5b rather than a fragile JSONB query.
func (s *Service) Prepare(ctx context.Context, subjectID uuid.UUID) (blocked bool, reasons []string, err error) {

	var nonZero int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM accounts a JOIN account_balances b ON b.account_id = a.id
		WHERE a.owner_id = $1 AND a.owner_type = 'user' AND b.balance != 0`, subjectID).Scan(&nonZero); err != nil {
		return false, nil, fmt.Errorf("ledger closure prepare: balance check: %w", err)
	}
	if nonZero > 0 {
		reasons = append(reasons, fmt.Sprintf("%d account(s) have a non-zero balance", nonZero))
	}

	var pendingTx int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM ledger_transactions t
		WHERE t.status = 'pending' AND (
			t.source_account_id IN (SELECT id FROM accounts WHERE owner_id = $1 AND owner_type = 'user') OR
			t.destination_account_id IN (SELECT id FROM accounts WHERE owner_id = $1 AND owner_type = 'user')
		)`, subjectID).Scan(&pendingTx); err != nil {
		return false, nil, fmt.Errorf("ledger closure prepare: pending transaction check: %w", err)
	}
	if pendingTx > 0 {
		reasons = append(reasons, fmt.Sprintf("%d transaction(s) are still pending (open lifecycle)", pendingTx))
	}

	var activeSchedules int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM scheduled_transactions WHERE user_id = $1 AND status IN ('active','paused')`, subjectID).Scan(&activeSchedules); err != nil {
		return false, nil, fmt.Errorf("ledger closure prepare: schedule check: %w", err)
	}
	if activeSchedules > 0 {
		reasons = append(reasons, fmt.Sprintf("%d scheduled transaction(s) are still active or paused", activeSchedules))
	}

	var pendingDisbursements int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM disbursement_items WHERE user_id = $1 AND status = 'pending'`, subjectID).Scan(&pendingDisbursements); err != nil {
		return false, nil, fmt.Errorf("ledger closure prepare: disbursement check: %w", err)
	}
	if pendingDisbursements > 0 {
		reasons = append(reasons, fmt.Sprintf("%d disbursement item(s) are still pending", pendingDisbursements))
	}

	if len(reasons) > 0 {
		return true, reasons, nil
	}
	return false, nil, nil
}

// Commit replaces every account.owner_id the subject still owns with
// surrogateID. Idempotent WITHOUT needing its own checkpoint state: a
// repeat call's UPDATE simply matches zero rows (owner_id no longer
// equals subjectID after the first call), and the result is always
// RE-DERIVED from what the surrogate currently owns — never from "rows
// affected this specific call" — so calling this twice (or concurrently,
// racing itself) returns the exact same (resultHash, affectedCount) both
// times.
func (s *Service) Commit(ctx context.Context, subjectID, surrogateID uuid.UUID) (resultHash string, affectedCount int, err error) {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE accounts SET owner_id = $1, updated_at = now()
		WHERE owner_id = $2 AND owner_type = 'user'`, surrogateID, subjectID); err != nil {
		return "", 0, fmt.Errorf("ledger closure commit: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id FROM accounts WHERE owner_id = $1 AND owner_type = 'user' ORDER BY id`, surrogateID)
	if err != nil {
		return "", 0, fmt.Errorf("ledger closure commit result: %w", err)
	}
	defer rows.Close()

	h := sha256.New()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return "", 0, fmt.Errorf("ledger closure commit scan: %w", err)
		}
		h.Write([]byte(id.String()))
		affectedCount++
	}
	if err := rows.Err(); err != nil {
		return "", 0, fmt.Errorf("ledger closure commit iterate: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), affectedCount, nil
}

// exportTransactionRow is docs/roadmap/active/51 T4b's own hand-written export
// DTO — deliberately excludes idempotency_key (T3's own digest tombstone
// already governs that separately) and error_message (may echo internal
// diagnostic text). amount is minor units, same convention every other
// owner's export row uses.
type exportTransactionRow struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	TxType    string    `json:"tx_type"`
	Status    string    `json:"status"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
	CreatedAt time.Time `json:"created_at"`
}

// Export returns the subject's own ledger_transactions headers (never
// ledger_entries — those are immutable financial evidence, K4's own
// "never export raw evidence" boundary is unnecessary to test since a
// transaction header is already the user-facing summary) for every
// transaction touching an account the subject owns, as of cutoff.
func (s *Service) Export(ctx context.Context, subjectID uuid.UUID, cutoff time.Time) ([]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT t.id, t.type, t.status, t.amount, t.currency, t.created_at
		FROM ledger_transactions t
		WHERE t.created_at <= $2 AND (
			t.source_account_id IN (SELECT id FROM accounts WHERE owner_id = $1 AND owner_type = 'user') OR
			t.destination_account_id IN (SELECT id FROM accounts WHERE owner_id = $1 AND owner_type = 'user')
		)
		ORDER BY t.created_at, t.id`, subjectID, cutoff)
	if err != nil {
		return nil, fmt.Errorf("ledger export: transactions: %w", err)
	}
	defer rows.Close()

	var out []json.RawMessage
	for rows.Next() {
		var row exportTransactionRow
		row.Type = "ledger_transaction"
		if err := rows.Scan(&row.ID, &row.TxType, &row.Status, &row.Amount, &row.Currency, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("ledger export: scan transactions: %w", err)
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, fmt.Errorf("ledger export: encode transaction: %w", err)
		}
		out = append(out, encoded)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger export: iterate transactions: %w", err)
	}
	return out, nil
}
