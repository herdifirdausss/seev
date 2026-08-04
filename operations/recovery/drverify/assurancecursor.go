package drverify

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// maxEffectiveTimestampQueries mirrors each source's own ListAssuranceRecords
// ORDER BY key (fetchPayinRows/fetchPayoutRows's effective_updated_at) —
// just the MAX of it, not a full paginated scan, since this check only
// needs one bound per source.
var maxEffectiveTimestampQueries = map[string]string{
	"payin": `SELECT GREATEST(COALESCE(MAX(i.updated_at), '-infinity'), COALESCE((SELECT MAX(updated_at) FROM payin_webhook_events), '-infinity')) FROM payin_topup_intents i`,
	"payout": `SELECT COALESCE(MAX(g), '-infinity') FROM (
		SELECT GREATEST(p.updated_at,
		    COALESCE((SELECT max(created_at) FROM payout_vendor_calls c WHERE c.payout_request_id = p.id), p.updated_at),
		    COALESCE((SELECT max(updated_at) FROM payout_vendor_commands c WHERE c.payout_request_id = p.id), p.updated_at)) AS g
		FROM payout_requests p) x`,
	"ledger": `SELECT COALESCE(MAX(updated_at), '-infinity') FROM ledger_transactions`,
}

// checkAssuranceCursor compares assurance_cursors' own bookmark
// (source, updated_at) against the maximum effective timestamp actually
// present in that source's restored database right now. A cursor ahead
// of its source means the two databases were not restored to the same
// consistent point — K9's "a cluster replay timestamp or LSN outside the
// selected recovery target", fatal. A cursor at or behind its source is
// exactly the normal, expected state ("assurance simply hasn't scanned
// this far yet") and produces no finding at all.
func checkAssuranceCursor(ctx context.Context, dbs map[string]*sql.DB, cfg Config, report *Report) error {
	assuranceDB, ok := dbs["assurance"]
	if !ok {
		return fmt.Errorf("assurance database connection unavailable")
	}
	type cursorRow struct {
		source    string
		updatedAt time.Time
		valid     bool
	}
	var cursors []cursorRow
	if err := readOnlyQuery(ctx, assuranceDB, cfg, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT source, updated_at FROM assurance_cursors`)
		if err != nil {
			return fmt.Errorf("read assurance_cursors: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var c cursorRow
			var updatedAt sql.NullTime
			if err := rows.Scan(&c.source, &updatedAt); err != nil {
				return fmt.Errorf("scan assurance cursor: %w", err)
			}
			c.updatedAt, c.valid = updatedAt.Time, updatedAt.Valid
			cursors = append(cursors, c)
		}
		var stuckRuns int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM assurance_runs WHERE status = 'running'`).Scan(&stuckRuns); err != nil {
			return fmt.Errorf("count stuck assurance runs: %w", err)
		}
		if stuckRuns > 0 {
			report.addSummary(Summary{
				Service: "assurance", Metric: "runs_stuck_running", Count: stuckRuns,
				Owner: "assurance-service (next scheduled run supersedes it)",
			})
		}
		return rows.Err()
	}); err != nil {
		return err
	}

	for _, c := range cursors {
		if !c.valid {
			continue
		}
		sourceDB, ok := dbs[c.source]
		if !ok {
			continue
		}
		query, ok := maxEffectiveTimestampQueries[c.source]
		if !ok {
			continue
		}
		var maxTimestamp time.Time
		if err := readOnlyQuery(ctx, sourceDB, cfg, func(ctx context.Context, tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, query).Scan(&maxTimestamp)
		}); err != nil {
			return fmt.Errorf("source %s max timestamp: %w", c.source, err)
		}
		if c.updatedAt.After(maxTimestamp) {
			report.addFinding(Finding{
				Code: "ASSURANCE_CURSOR_AHEAD_OF_SOURCE", Severity: SeverityFatal, Service: "assurance",
				ResourceID: c.source,
				Message:    fmt.Sprintf("assurance_cursors.%s cursor is ahead of the restored source database — inconsistent restore point", c.source),
				Evidence:   map[string]string{"cursor_updated_at": c.updatedAt.Format(time.RFC3339Nano), "source_max_updated_at": maxTimestamp.Format(time.RFC3339Nano)},
			})
		}
	}
	return nil
}
