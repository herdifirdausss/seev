package drverify

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/herdifirdausss/seev/internal/assurance/rules"
)

type payinRow struct {
	id, recordType, status, userID                           string
	amountMinor                                              int64
	currency, vendor, reference, externalRef, settledEventID string
	requestIDPresent                                         bool
	ledgerType, ledgerGateway                                string
	effectiveUpdatedAt, createdAt                            time.Time
}

// fetchPayinRows mirrors internal/payin/assurance.go's ListAssuranceRecords
// query exactly (same UNION of topup_intents/webhook_events, same
// correlation LATERAL for an intent's settled webhook) but as one bounded,
// paginated full-table pass rather than an incrementally-resumable scan —
// drverify makes a single verification pass, it does not need cursor
// state across runs. Each round-trip fetches at most cfg.PageSize rows
// (K9 "bounded batches"); the returned slice accumulates every page, since
// correlating an intent against its settled webhook (possibly on a
// different page) needs the full set in memory. docs/roadmap/active/50 §8
// explicitly disclaims any production-scale guarantee for this track —
// this is the concrete place that trade-off is made.
func fetchPayinRows(ctx context.Context, tx *sql.Tx, pageSize int) ([]payinRow, error) {
	const query = `
		SELECT id, record_type, effective_updated_at, created_at, status, user_id,
		       amount, currency, vendor, reference, external_ref, settled_event_id,
		       request_id_present, ledger_type, ledger_gateway
			FROM (
			SELECT i.id, 'intent' AS record_type, i.updated_at AS effective_updated_at,
			       i.created_at, i.status, i.user_id, i.amount, i.currency, i.vendor,
			       i.reference, COALESCE(e.external_ref, '') AS external_ref,
			       COALESCE(i.settled_event_id::text, '') AS settled_event_id,
			       (i.request_id IS NOT NULL AND i.request_id <> '') AS request_id_present,
			       CASE WHEN e.id IS NULL THEN '' ELSE 'money_in' END AS ledger_type,
			       COALESCE(vg.gateway, e.vendor, '') AS ledger_gateway
			FROM payin_topup_intents i
			LEFT JOIN LATERAL (
				SELECT e.*
				FROM payin_webhook_events e
				WHERE e.id = i.settled_event_id
				   OR (i.reference <> '' AND e.status = 'posted' AND e.user_id = i.user_id
				       AND e.amount = i.amount AND e.currency = i.currency AND e.external_ref = i.reference)
				ORDER BY (e.id = i.settled_event_id) DESC, e.updated_at DESC, e.id DESC
				LIMIT 1
			) e ON true
			LEFT JOIN payin_vendor_gateways vg ON vg.vendor = e.vendor
			UNION ALL
			SELECT e.id, 'webhook_event', e.updated_at, e.created_at, e.status, e.user_id,
			       e.amount, e.currency, e.vendor, '', e.external_ref, '',
			       (e.request_id IS NOT NULL AND e.request_id <> ''), 'money_in', COALESCE(vg.gateway, e.vendor, '')
			FROM payin_webhook_events e
			LEFT JOIN payin_vendor_gateways vg ON vg.vendor = e.vendor
		) assurance
		WHERE effective_updated_at <= now()
		  AND (effective_updated_at, id) > ($1, $2)
		ORDER BY effective_updated_at ASC, id ASC
		LIMIT $3`

	var all []payinRow
	// Real bug found live: an empty-string cursorID fails Postgres's
	// parameter type-check for the row-value comparison below (the
	// planner infers a uuid type for $2 since it's compared against the
	// id column, and "" is not a valid uuid literal) — before any row is
	// even evaluated, not merely a runtime no-match. The nil UUID sorts
	// before every real id, same effect, valid literal.
	cursorTime, cursorID := time.Time{}, "00000000-0000-0000-0000-000000000000"
	for {
		rows, err := tx.QueryContext(ctx, query, cursorTime, cursorID, pageSize)
		if err != nil {
			return nil, fmt.Errorf("payin verification query: %w", err)
		}
		page := 0
		for rows.Next() {
			var r payinRow
			if err := rows.Scan(&r.id, &r.recordType, &r.effectiveUpdatedAt, &r.createdAt, &r.status, &r.userID,
				&r.amountMinor, &r.currency, &r.vendor, &r.reference, &r.externalRef, &r.settledEventID,
				&r.requestIDPresent, &r.ledgerType, &r.ledgerGateway); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan payin row: %w", err)
			}
			all = append(all, r)
			cursorTime, cursorID = r.effectiveUpdatedAt, r.id
			page++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		if page < pageSize {
			break
		}
	}
	return all, nil
}

// checkPayin correlates every payin intent/webhook against its ledger
// proof (queried live from seev_ledger, not gRPC) and runs
// assurancerules.EvaluatePayin — the exact invariant logic
// internal/assurance runs live, reused here per K9's explicit DTO-sharing
// allowance. Valid in-flight state (e.g. a still-pending intent within
// its consistency delay) produces zero findings, exactly as it does live
// — it is not itself wrong, so it is not reported as one.
func checkPayin(ctx context.Context, payinDB, ledgerDB *sql.DB, cfg Config, report *Report) error {
	var rows []payinRow
	if err := readOnlyQuery(ctx, payinDB, cfg, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		rows, err = fetchPayinRows(ctx, tx, cfg.PageSize)
		return err
	}); err != nil {
		return err
	}

	byID := make(map[string]payinRow, len(rows))
	for _, r := range rows {
		byID[r.id] = r
	}

	pendingCount := 0
	err := readOnlyQuery(ctx, ledgerDB, cfg, func(ctx context.Context, tx *sql.Tx) error {
		for _, r := range rows {
			var proofs []rules.LedgerProof
			if r.ledgerType != "" {
				proof, err := ledgerProofByCorrelation(ctx, tx, r.ledgerType, r.ledgerGateway, r.externalRef)
				if err != nil {
					return err
				}
				proofs = proof
			}
			record := rules.PayinRecord{
				ID: r.id, RecordType: r.recordType, Status: r.status, UserID: r.userID,
				AmountMinor: r.amountMinor, Currency: r.currency, Vendor: r.vendor, Reference: r.reference,
				ExternalRef: r.externalRef, SettledEventID: r.settledEventID, RequestIDPresent: r.requestIDPresent,
				Age: time.Since(r.createdAt), Ledger: proofs, ConsistencyDelay: 2 * time.Minute,
			}
			if linked, ok := byID[r.settledEventID]; ok {
				record.SettledWebhook = &rules.PayinRecord{ID: linked.id, RecordType: linked.recordType, Status: linked.status, UserID: linked.userID, AmountMinor: linked.amountMinor, Currency: linked.currency, Reference: linked.reference, ExternalRef: linked.externalRef}
			}
			if r.recordType == "intent" && r.status == "pending" {
				pendingCount++
			}
			for _, f := range rules.EvaluatePayin(record) {
				report.addFinding(classifyAssuranceFinding(f, "payin"))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if pendingCount > 0 {
		report.addSummary(Summary{
			Service: "payin", Metric: "pending_topup_intents", Count: pendingCount,
			Owner: "payin-service worker (retries automatically)",
		})
	}
	return nil
}
