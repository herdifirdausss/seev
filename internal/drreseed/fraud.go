package drreseed

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/herdifirdausss/seev/internal/fraud"
	"github.com/herdifirdausss/seev/internal/fraud/rules"
	"github.com/herdifirdausss/seev/internal/ledger/events"
)

// velocityTTL mirrors internal/fraud/consumer.go's own unexported
// velocityTTL constant exactly (2 * time.Hour) — duplicated here rather
// than exported, since (unlike the policy key builders) this one value
// is trivial and the actual atomicity/format guarantee comes from
// reusing fraud.RedisVelocityStore.Record's Lua script directly below,
// not from re-deriving key strings by hand.
const velocityTTL = 2 * time.Hour

// ReconstructFraudVelocity rebuilds the current hour's velocity counters
// and every dedup marker for posted-transaction events within the active
// two-hour TTL window (K10 item 3), using the EXACT SAME
// fraud.RedisVelocityStore.Record the live consumer calls — same atomic
// Lua script (SET NX dedup, then INCR+EXPIRE the counter), so a
// reconstructed key is bit-for-bit indistinguishable from one the live
// consumer would have written.
//
// Fails closed (K10: "If required source evidence is unavailable, the
// fraud path remains fail-closed and the gate fails") rather than
// reconstructing a partial/undercounted state: before writing anything,
// it confirms every posted ledger_transaction in the window has a
// matching published `ledger.transaction.posted.v1` outbox event. A gap
// there means the evidence this reconstruction depends on is itself
// incomplete (e.g. outbox_events pruned/compacted) — silently
// under-counting velocity would be actively dangerous (a user who should
// be rate-limited by fraud rules would not be), so this returns an error
// instead of writing anything.
func ReconstructFraudVelocity(ctx context.Context, ledgerDB *sql.DB, store fraud.VelocityStore, report *Report) error {
	windowStart := time.Now().Add(-velocityTTL)

	var postedCount, evidencedCount int
	err := ledgerDB.QueryRowContext(ctx, `SELECT count(*) FROM ledger_transactions WHERE status = 'posted' AND created_at >= $1`, windowStart).Scan(&postedCount)
	if err != nil {
		return fmt.Errorf("count posted transactions in window: %w", err)
	}
	err = ledgerDB.QueryRowContext(ctx, `
		SELECT count(DISTINCT lt.id)
		FROM ledger_transactions lt
		JOIN outbox_events oe ON oe.aggregate_id = lt.id AND oe.event_type = $1 AND oe.status = 'published'
		WHERE lt.status = 'posted' AND lt.created_at >= $2`, events.TypeTransactionPosted, windowStart).Scan(&evidencedCount)
	if err != nil {
		return fmt.Errorf("count evidenced transactions in window: %w", err)
	}
	if evidencedCount < postedCount {
		return fmt.Errorf("fraud reconstruction evidence incomplete: %d posted transactions in the active window, only %d have a published outbox event — refusing to reconstruct a partial/undercounted velocity state (fail-closed per K10)", postedCount, evidencedCount)
	}

	rows, err := ledgerDB.QueryContext(ctx, `
		SELECT oe.id, oe.payload
		FROM outbox_events oe
		WHERE oe.event_type = $1 AND oe.status = 'published' AND oe.created_at >= $2
		ORDER BY oe.created_at`, events.TypeTransactionPosted, windowStart)
	if err != nil {
		return fmt.Errorf("query outbox events: %w", err)
	}
	defer rows.Close()

	type rawEvent struct {
		id      string
		payload []byte
	}
	var rawEvents []rawEvent
	for rows.Next() {
		var e rawEvent
		if err := rows.Scan(&e.id, &e.payload); err != nil {
			return fmt.Errorf("scan outbox event: %w", err)
		}
		rawEvents = append(rawEvents, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, raw := range rawEvents {
		var payload events.TransactionPosted
		if err := json.Unmarshal(raw.payload, &payload); err != nil {
			return fmt.Errorf("decode outbox event %s payload: %w", raw.id, err)
		}
		// Matches internal/fraud/consumer.go's handleDelivery exactly: an
		// event with no acting user (an internal system-only posting) is
		// not a velocity signal.
		if payload.UserID == nil {
			continue
		}
		at := payload.OccurredAt
		if at.IsZero() {
			at = time.Now()
		}
		key := rules.VelocityKey(payload.UserID.String(), at)
		if err := store.Record(ctx, raw.id, key, velocityTTL); err != nil {
			return fmt.Errorf("record velocity for event %s: %w", raw.id, err)
		}
		report.FraudEventsReplayed++
		if at.UTC().Format("2006-01-02-15") == time.Now().UTC().Format("2006-01-02-15") {
			report.FraudCurrentHourEvents++
		}
	}
	return nil
}
