// Package payin's own owner-side of docs/roadmap/active/51-a8-data-lifecycle-privacy.md T4b/T5b
// (K9, K10, K11) — the export and closure contracts auth-service calls
// into. Mirrors internal/ledger/service/closure's own Prepare/Commit
// shape (docs/roadmap/active/51 T5) and internal/auth's own export DTO
// convention (hand-written row types, explicit type/exclusions), applied
// to payin's three user-referencing tables.
package payin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PrivacyPrepareClosure checks K10's payin-owned blocking condition: a
// topup intent still 'pending' (money may be in flight to a vendor that
// hasn't settled yet — closing now would strand it). Read-only, unlocked,
// same fast-fail-UX-not-enforcement-point posture as ledger's own Prepare.
func (m *Module) PrivacyPrepareClosure(ctx context.Context, subjectID uuid.UUID) (blocked bool, reasons []string, err error) {
	var pendingIntents int
	if err := m.db.QueryRowContext(ctx, `
		SELECT count(*) FROM payin_topup_intents WHERE user_id = $1 AND status = 'pending'`, subjectID).Scan(&pendingIntents); err != nil {
		return false, nil, fmt.Errorf("payin closure prepare: %w", err)
	}
	if pendingIntents > 0 {
		return true, []string{fmt.Sprintf("%d topup intent(s) still pending settlement", pendingIntents)}, nil
	}
	return false, nil, nil
}

// PrivacyCommitClosure repoints user_id on every payin table that
// references the subject to the surrogate. Idempotent WITHOUT its own
// checkpoint, same convention as ledger's Commit: the result is always
// re-derived from what the surrogate currently owns (sorted row ids,
// hashed), never from "rows affected this specific call".
func (m *Module) PrivacyCommitClosure(ctx context.Context, subjectID, surrogateID uuid.UUID) (resultHash string, affectedCount int, err error) {
	if _, err := m.db.ExecContext(ctx, `UPDATE payin_webhook_events SET user_id = $1, updated_at = now() WHERE user_id = $2`, surrogateID, subjectID); err != nil {
		return "", 0, fmt.Errorf("payin closure commit: webhook_events: %w", err)
	}
	if _, err := m.db.ExecContext(ctx, `UPDATE payin_topup_intents SET user_id = $1, updated_at = now() WHERE user_id = $2`, surrogateID, subjectID); err != nil {
		return "", 0, fmt.Errorf("payin closure commit: topup_intents: %w", err)
	}
	if _, err := m.db.ExecContext(ctx, `UPDATE payin_routing_rules SET user_id = $1, updated_at = now() WHERE user_id = $2`, surrogateID, subjectID); err != nil {
		return "", 0, fmt.Errorf("payin closure commit: routing_rules: %w", err)
	}

	h := sha256.New()
	for _, q := range []string{
		`SELECT id FROM payin_webhook_events WHERE user_id = $1 ORDER BY id`,
		`SELECT id FROM payin_topup_intents WHERE user_id = $1 ORDER BY id`,
		`SELECT id FROM payin_routing_rules WHERE user_id = $1 ORDER BY id`,
	} {
		rows, err := m.db.QueryContext(ctx, q, surrogateID)
		if err != nil {
			return "", 0, fmt.Errorf("payin closure commit result: %w", err)
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return "", 0, fmt.Errorf("payin closure commit scan: %w", err)
			}
			h.Write([]byte(id.String()))
			affectedCount++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", 0, fmt.Errorf("payin closure commit iterate: %w", err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), affectedCount, nil
}

// privacyExportWebhookEventRow/privacyExportTopupIntentRow are docs/roadmap/active/51
// T4b's own hand-written export DTOs (never a struct reused from another
// layer, matching internal/auth's own T4 convention) — deliberately
// excludes `raw`/`raw_ciphertext` (T2.4's own encrypted vendor payload,
// never fit for export) and `error_message` (may itself echo vendor-sent
// text).
type privacyExportWebhookEventRow struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Vendor    string    `json:"vendor"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type privacyExportTopupIntentRow struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Reference string    `json:"reference"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
	Vendor    string    `json:"vendor"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// PrivacyExportRows returns the subject's own payin rows as of cutoff —
// K9's own owner-composed export contract. Ordered deterministically
// (created_at, id), same as auth's own collectAuthOwnerRows.
func (m *Module) PrivacyExportRows(ctx context.Context, subjectID uuid.UUID, cutoff time.Time) ([]json.RawMessage, error) {
	var out []json.RawMessage

	eventRows, err := m.db.QueryContext(ctx, `
		SELECT id, vendor, amount, currency, status, created_at FROM payin_webhook_events
		WHERE user_id = $1 AND created_at <= $2 ORDER BY created_at, id`, subjectID, cutoff)
	if err != nil {
		return nil, fmt.Errorf("payin export: webhook_events: %w", err)
	}
	for eventRows.Next() {
		var row privacyExportWebhookEventRow
		row.Type = "payin_webhook_event"
		if err := eventRows.Scan(&row.ID, &row.Vendor, &row.Amount, &row.Currency, &row.Status, &row.CreatedAt); err != nil {
			eventRows.Close()
			return nil, fmt.Errorf("payin export: scan webhook_events: %w", err)
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			eventRows.Close()
			return nil, fmt.Errorf("payin export: encode webhook_event: %w", err)
		}
		out = append(out, encoded)
	}
	if err := eventRows.Err(); err != nil {
		eventRows.Close()
		return nil, fmt.Errorf("payin export: iterate webhook_events: %w", err)
	}
	eventRows.Close()

	intentRows, err := m.db.QueryContext(ctx, `
		SELECT id, reference, amount, currency, vendor, status, created_at FROM payin_topup_intents
		WHERE user_id = $1 AND created_at <= $2 ORDER BY created_at, id`, subjectID, cutoff)
	if err != nil {
		return nil, fmt.Errorf("payin export: topup_intents: %w", err)
	}
	defer intentRows.Close()
	for intentRows.Next() {
		var row privacyExportTopupIntentRow
		row.Type = "payin_topup_intent"
		if err := intentRows.Scan(&row.ID, &row.Reference, &row.Amount, &row.Currency, &row.Vendor, &row.Status, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("payin export: scan topup_intents: %w", err)
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, fmt.Errorf("payin export: encode topup_intent: %w", err)
		}
		out = append(out, encoded)
	}
	if err := intentRows.Err(); err != nil {
		return nil, fmt.Errorf("payin export: iterate topup_intents: %w", err)
	}
	return out, nil
}
