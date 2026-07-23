package drverify

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/herdifirdausss/seev/internal/assurance/rules"
)

type payoutRow struct {
	id, status, userID, currency, vendor string
	amountMinor, feeAmountMinor          int64
	holdTxID, settleTxID, feeQuoteID     string
	feeGateway                           string
	requestIDPresent                     bool
	effectiveUpdatedAt, createdAt        time.Time
}

// fetchPayoutRows mirrors internal/payout/assurance.go's
// ListAssuranceRecords query exactly (same GREATEST-of-three effective-
// timestamp shape) as one bounded, paginated full-table pass — see
// fetchPayinRows's doc comment for why this differs from the live
// service's incrementally-resumable version.
func fetchPayoutRows(ctx context.Context, tx *sql.Tx, pageSize int) ([]payoutRow, error) {
	const query = `
		SELECT p.id, p.created_at, p.status, p.user_id, p.amount, p.currency, p.vendor,
		       COALESCE(p.hold_tx_id::text, ''), COALESCE(p.settle_tx_id::text, ''),
		       (p.request_id IS NOT NULL AND p.request_id <> ''),
		       COALESCE(p.fee_quote_id::text, ''), COALESCE(p.fee_amount, 0),
		       COALESCE(p.fee_gateway, ''),
		       GREATEST(p.updated_at,
		          COALESCE((SELECT max(created_at) FROM payout_vendor_calls c WHERE c.payout_request_id = p.id), p.updated_at),
		          COALESCE((SELECT max(updated_at) FROM payout_vendor_commands c WHERE c.payout_request_id = p.id), p.updated_at)) AS effective_updated_at
		FROM payout_requests p
		WHERE GREATEST(p.updated_at,
		          COALESCE((SELECT max(created_at) FROM payout_vendor_calls c WHERE c.payout_request_id = p.id), p.updated_at),
		          COALESCE((SELECT max(updated_at) FROM payout_vendor_commands c WHERE c.payout_request_id = p.id), p.updated_at)) <= now()
		  AND (GREATEST(p.updated_at,
		          COALESCE((SELECT max(created_at) FROM payout_vendor_calls c WHERE c.payout_request_id = p.id), p.updated_at),
		          COALESCE((SELECT max(updated_at) FROM payout_vendor_commands c WHERE c.payout_request_id = p.id), p.updated_at)), p.id) > ($1, $2)
		ORDER BY effective_updated_at ASC, p.id ASC LIMIT $3`

	var all []payoutRow
	// See fetchPayinRows's identical fix for why this must be a valid
	// nil-UUID literal, not an empty string.
	cursorTime, cursorID := time.Time{}, "00000000-0000-0000-0000-000000000000"
	for {
		rows, err := tx.QueryContext(ctx, query, cursorTime, cursorID, pageSize)
		if err != nil {
			return nil, fmt.Errorf("payout verification query: %w", err)
		}
		page := 0
		for rows.Next() {
			var r payoutRow
			if err := rows.Scan(&r.id, &r.createdAt, &r.status, &r.userID, &r.amountMinor, &r.currency, &r.vendor,
				&r.holdTxID, &r.settleTxID, &r.requestIDPresent, &r.feeQuoteID, &r.feeAmountMinor, &r.feeGateway,
				&r.effectiveUpdatedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan payout row: %w", err)
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

func fetchVendorCalls(ctx context.Context, tx *sql.Tx, payoutID string) ([]rules.VendorCall, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT attempt, COALESCE(NULLIF(substring(req_summary FROM 'vendor=([^ ]+)'), ''), '') AS vendor, outcome, created_at
		 FROM payout_vendor_calls WHERE payout_request_id = $1 ORDER BY attempt, created_at`, payoutID)
	if err != nil {
		return nil, fmt.Errorf("vendor calls lookup: %w", err)
	}
	defer rows.Close()
	var out []rules.VendorCall
	for rows.Next() {
		var c rules.VendorCall
		if err := rows.Scan(&c.Attempt, &c.Vendor, &c.Outcome, &c.At); err != nil {
			return nil, fmt.Errorf("scan vendor call: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// fetchVendorCommands also returns the raw status/count breakdown so
// checkVendorCommands (a nominally separate T4 check category) can be
// satisfied from the SAME query rather than a redundant second pass —
// see that function's own doc comment for why.
func fetchVendorCommands(ctx context.Context, tx *sql.Tx, payoutID string) ([]rules.VendorCommand, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, vendor, attempt, status FROM payout_vendor_commands WHERE payout_request_id = $1 ORDER BY attempt, id`, payoutID)
	if err != nil {
		return nil, fmt.Errorf("vendor commands lookup: %w", err)
	}
	defer rows.Close()
	var out []rules.VendorCommand
	for rows.Next() {
		var c rules.VendorCommand
		if err := rows.Scan(&c.ID, &c.Vendor, &c.Attempt, &c.Status); err != nil {
			return nil, fmt.Errorf("scan vendor command: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func fetchFeeProof(ctx context.Context, tx *sql.Tx, quoteID string) (*rules.FeeProof, error) {
	if quoteID == "" {
		return nil, nil
	}
	var amount, feeAmount int64
	var gateway, txType string
	var consumedBy sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT amount, transaction_type, fee_amount, fee_gateway, consumed_by_ref FROM fee_quotes WHERE id = $1`, quoteID).
		Scan(&amount, &txType, &feeAmount, &gateway, &consumedBy)
	if err == sql.ErrNoRows {
		return &rules.FeeProof{Exists: false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fee quote lookup: %w", err)
	}
	return &rules.FeeProof{Exists: true, ConsumedByRef: consumedBy.String, AmountMinor: feeAmount, Gateway: gateway, TransactionType: txType}, nil
}

// checkPayout correlates every payout request against its ledger hold/
// close proofs, vendor call/command history, and fee quote, then runs
// assurancerules.EvaluatePayout — the same reused invariant logic
// checkPayin uses for the payin side. This single pass also satisfies T4's
// separately-named "vendor-command" check category: PO04/PO05/PO06 in
// EvaluatePayout already classify every vendor-command state a second,
// standalone pass over payout_vendor_commands would otherwise re-derive
// from the identical rows — a redundant second query, not a different
// invariant.
func checkPayout(ctx context.Context, payoutDB, ledgerDB *sql.DB, cfg Config, report *Report) error {
	var rows []payoutRow
	if err := readOnlyQuery(ctx, payoutDB, cfg, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		rows, err = fetchPayoutRows(ctx, tx, cfg.PageSize)
		return err
	}); err != nil {
		return err
	}

	type children struct {
		calls    []rules.VendorCall
		commands []rules.VendorCommand
		fee      *rules.FeeProof
	}
	childrenByID := make(map[string]children, len(rows))
	inFlight := 0
	deadCommands := 0
	err := readOnlyQuery(ctx, payoutDB, cfg, func(ctx context.Context, tx *sql.Tx) error {
		for _, r := range rows {
			calls, err := fetchVendorCalls(ctx, tx, r.id)
			if err != nil {
				return err
			}
			commands, err := fetchVendorCommands(ctx, tx, r.id)
			if err != nil {
				return err
			}
			fee, err := fetchFeeProof(ctx, tx, r.feeQuoteID)
			if err != nil {
				return err
			}
			childrenByID[r.id] = children{calls: calls, commands: commands, fee: fee}
			terminal := r.status == "settled" || r.status == "cancelled" || r.status == "failed" || r.status == "rejected"
			if !terminal {
				inFlight++
			}
			for _, c := range commands {
				if c.Status == "dead" {
					deadCommands++
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	err = readOnlyQuery(ctx, ledgerDB, cfg, func(ctx context.Context, tx *sql.Tx) error {
		for _, r := range rows {
			hold, err := ledgerProofByID(ctx, tx, r.holdTxID)
			if err != nil {
				return err
			}
			closing, err := ledgerProofByID(ctx, tx, r.settleTxID)
			if err != nil {
				return err
			}
			c := childrenByID[r.id]
			record := rules.PayoutRecord{
				ID: r.id, Status: r.status, AmountMinor: r.amountMinor, Currency: r.currency, Vendor: r.vendor,
				HoldTxID: r.holdTxID, SettleTxID: r.settleTxID, FeeQuoteID: r.feeQuoteID, FeeAmountMinor: r.feeAmountMinor,
				FeeGateway: r.feeGateway, Age: time.Since(r.createdAt), RequestIDPresent: r.requestIDPresent,
				Hold: hold, Closing: closing, VendorCalls: c.calls, VendorCommands: c.commands, FeeQuote: c.fee,
			}
			if closing != nil {
				record.BookedFeeMinor, record.BookedFeeGateway = closing.BookedFeeMinor, closing.BookedFeeGateway
			}
			for _, f := range rules.EvaluatePayout(record) {
				report.addFinding(classifyAssuranceFinding(f, "payout"))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if inFlight > 0 {
		report.addSummary(Summary{
			Service: "payout", Metric: "in_flight_requests", Count: inFlight,
			Owner: "payout-service worker (resumes automatically)",
		})
	}
	if deadCommands > 0 {
		report.addSummary(Summary{
			Service: "payout", Metric: "dead_vendor_commands", Count: deadCommands,
			Owner: "operator (GET /admin/payout/vendor-commands/dead, ReplayDeadCommand)",
		})
	}
	return nil
}
