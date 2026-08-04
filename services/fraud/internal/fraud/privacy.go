// Package fraud's own owner-side of docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T4b/T5b
// (K9, K10, K11) — mirrors services/payin's own privacy.go shape.
package fraud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/lifecycle/privacy"
)

// PrivacyPrepareClosure: screening_events are purely historical (a
// verdict already rendered, nothing "in flight" to strand) — fraud has no
// K10 blocking condition of its own.
func (m *Module) PrivacyPrepareClosure(ctx context.Context, subjectID uuid.UUID) (blocked bool, reasons []string, err error) {
	return false, nil, nil
}

// PrivacyCommitClosure repoints screening_events.user_id to the surrogate
// via a SECURITY DEFINER function (services/fraud/migrations/000006) — that table
// deliberately grants app_service no UPDATE (append-only audit
// philosophy), so the repoint is channeled through this one narrow
// function rather than widening the table's own grant. Idempotent:
// re-running finds zero rows still owned by subject and simply reports 0
// affected on a repeat call — no separate checkpoint needed. Result hash
// is re-derived from what the surrogate currently owns, same convention
// as every other owner's Commit.
func (m *Module) PrivacyCommitClosure(ctx context.Context, subjectID, surrogateID uuid.UUID) (resultHash string, affectedCount int, err error) {
	if _, err := m.db.ExecContext(ctx, `SELECT fn_privacy_closure_repoint_screening_events($1, $2)`, subjectID, surrogateID); err != nil {
		return "", 0, fmt.Errorf("fraud closure commit: %w", err)
	}

	rows, err := m.db.QueryContext(ctx, `SELECT id FROM screening_events WHERE user_id = $1 ORDER BY id`, surrogateID)
	if err != nil {
		return "", 0, fmt.Errorf("fraud closure commit result: %w", err)
	}
	defer rows.Close()

	h := sha256.New()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return "", 0, fmt.Errorf("fraud closure commit scan: %w", err)
		}
		h.Write([]byte(id.String()))
		affectedCount++
	}
	if err := rows.Err(); err != nil {
		return "", 0, fmt.Errorf("fraud closure commit iterate: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), affectedCount, nil
}

// privacyExportScreeningEventRow is docs/roadmap/archive/51 T4b's own hand-written
// export DTO. `reason` is a short rule-generated string (e.g.
// "amount exceeds threshold"), never vendor/free-text user input, so it's
// safe to include unlike payin/payout's own excluded free-text fields.
type privacyExportScreeningEventRow struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	TxType    string    `json:"tx_type"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
	Verdict   string    `json:"verdict"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// PrivacyExportRows returns the subject's own screening events as of cutoff.
func (m *Module) PrivacyExportRows(ctx context.Context, subjectID uuid.UUID, cutoff time.Time) ([]json.RawMessage, error) {
	var all []json.RawMessage
	for offset := 0; ; {
		page, next, err := m.PrivacyExportPage(ctx, subjectID, cutoff, offset, privacyexport.DefaultPageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if next == "" {
			return all, nil
		}
		offset += len(page)
	}
}

func (m *Module) PrivacyExportPage(ctx context.Context, subjectID uuid.UUID, cutoff time.Time, offset, pageSize int) ([]json.RawMessage, string, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, tx_type, amount, currency, verdict, reason, created_at FROM screening_events
		WHERE user_id = $1 AND created_at <= $2 ORDER BY created_at, id
		LIMIT $3 OFFSET $4`, subjectID, cutoff, pageSize+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("fraud export: screening_events: %w", err)
	}
	defer rows.Close()

	var out []json.RawMessage
	for rows.Next() {
		var row privacyExportScreeningEventRow
		row.Type = "screening_event"
		if err := rows.Scan(&row.ID, &row.TxType, &row.Amount, &row.Currency, &row.Verdict, &row.Reason, &row.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("fraud export: scan screening_events: %w", err)
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, "", fmt.Errorf("fraud export: encode screening_event: %w", err)
		}
		out = append(out, encoded)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("fraud export: iterate screening_events: %w", err)
	}
	hasMore := len(out) > pageSize
	if hasMore {
		out = out[:pageSize]
	}
	return out, privacyexport.Next(offset, len(out), hasMore), nil
}
