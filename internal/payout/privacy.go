// Package payout's own owner-side of docs/roadmap/active/51-a8-data-lifecycle-privacy.md T4b/T5b
// (K9, K10, K11) — mirrors internal/payin's own privacy.go shape.
package payout

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PrivacyPrepareClosure checks K10's payout-owned blocking condition: an
// open withdrawal lifecycle — any payout_requests row not yet in a
// terminal status (settled/failed/cancelled) means money is held or
// mid-flight to a vendor.
func (m *Module) PrivacyPrepareClosure(ctx context.Context, subjectID uuid.UUID) (blocked bool, reasons []string, err error) {
	var openRequests int
	if err := m.db.QueryRowContext(ctx, `
		SELECT count(*) FROM payout_requests
		WHERE user_id = $1 AND status NOT IN ('settled', 'failed', 'cancelled')`, subjectID).Scan(&openRequests); err != nil {
		return false, nil, fmt.Errorf("payout closure prepare: %w", err)
	}
	if openRequests > 0 {
		return true, []string{fmt.Sprintf("%d payout request(s) have an open withdrawal lifecycle", openRequests)}, nil
	}
	return false, nil, nil
}

// PrivacyCommitClosure repoints user_id on payout_requests and
// payout_routing_rules to the surrogate — idempotent without its own
// checkpoint, same re-derive-from-current-state convention as ledger's
// and payin's own Commit.
func (m *Module) PrivacyCommitClosure(ctx context.Context, subjectID, surrogateID uuid.UUID) (resultHash string, affectedCount int, err error) {
	if _, err := m.db.ExecContext(ctx, `UPDATE payout_requests SET user_id = $1, updated_at = now() WHERE user_id = $2`, surrogateID, subjectID); err != nil {
		return "", 0, fmt.Errorf("payout closure commit: requests: %w", err)
	}
	if _, err := m.db.ExecContext(ctx, `UPDATE payout_routing_rules SET user_id = $1, updated_at = now() WHERE user_id = $2`, surrogateID, subjectID); err != nil {
		return "", 0, fmt.Errorf("payout closure commit: routing_rules: %w", err)
	}

	h := sha256.New()
	for _, q := range []string{
		`SELECT id FROM payout_requests WHERE user_id = $1 ORDER BY id`,
		`SELECT id FROM payout_routing_rules WHERE user_id = $1 ORDER BY id`,
	} {
		rows, err := m.db.QueryContext(ctx, q, surrogateID)
		if err != nil {
			return "", 0, fmt.Errorf("payout closure commit result: %w", err)
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return "", 0, fmt.Errorf("payout closure commit scan: %w", err)
			}
			h.Write([]byte(id.String()))
			affectedCount++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", 0, fmt.Errorf("payout closure commit iterate: %w", err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), affectedCount, nil
}

// privacyExportPayoutRequestRow is docs/roadmap/active/51 T4b's own hand-written
// export DTO — excludes `destination`/`destination_ciphertext` (T2.4's
// own encrypted bank/account destination, never fit for export) and
// `error_message`/`vendor_ref` (may echo vendor-sent text/internal
// correlation ids).
type privacyExportPayoutRequestRow struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
	Vendor    string    `json:"vendor"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// PrivacyExportRows returns the subject's own payout rows as of cutoff.
func (m *Module) PrivacyExportRows(ctx context.Context, subjectID uuid.UUID, cutoff time.Time) ([]json.RawMessage, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, amount, currency, vendor, status, created_at FROM payout_requests
		WHERE user_id = $1 AND created_at <= $2 ORDER BY created_at, id`, subjectID, cutoff)
	if err != nil {
		return nil, fmt.Errorf("payout export: requests: %w", err)
	}
	defer rows.Close()

	var out []json.RawMessage
	for rows.Next() {
		var row privacyExportPayoutRequestRow
		row.Type = "payout_request"
		if err := rows.Scan(&row.ID, &row.Amount, &row.Currency, &row.Vendor, &row.Status, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("payout export: scan requests: %w", err)
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, fmt.Errorf("payout export: encode request: %w", err)
		}
		out = append(out, encoded)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payout export: iterate requests: %w", err)
	}
	return out, nil
}
