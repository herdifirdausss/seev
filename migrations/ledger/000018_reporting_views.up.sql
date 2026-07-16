-- docs/plan/20 Task T2 (S7): regulatory/compliance reporting foundation —
-- three read-only aggregate views. Views are the query CONTRACT (reviewed
-- here, not re-derived ad-hoc in Go code) over snapshots (H3) + recon (H2).
--
-- security_invoker is left at its default (false) — DELIBERATE decision
-- (docs/plan/20 Task T2 step 1): app_readonly already has direct SELECT
-- grants (migrations/000009) on every table these views read (accounts,
-- account_balance_snapshots, ledger_transactions, recon_batches,
-- recon_items), so view-owner semantics don't grant app_readonly anything
-- it doesn't already have row-level access to here — this note exists so a
-- FUTURE view added to this file that touches a table app_readonly does NOT
-- have a direct grant on (outbox_events, pending_adjustments,
-- scheduled_transactions — all carry an internal payload/cmd_payload
-- column) gets its column list reviewed for the same reason before being
-- added. None of the three views below select any *payload/cmd_payload
-- column, or any column from outbox_events/pending_adjustments/
-- scheduled_transactions at all.

-- v_report_daily_position: fund position per day per currency per account
-- type/owner type — aggregates account_balance_snapshots (H3) JOIN accounts.
CREATE VIEW v_report_daily_position AS
SELECT
    s.as_of_date,
    a.currency,
    a.type        AS account_type,
    a.owner_type,
    count(*)                AS account_count,
    sum(s.closing_balance)  AS total_balance
FROM account_balance_snapshots s
JOIN accounts a ON a.id = s.account_id
GROUP BY s.as_of_date, a.currency, a.type, a.owner_type;

-- v_report_daily_mutation: posted transaction volume per day per type per
-- currency — WIB (Asia/Jakarta) calendar day, explicit timezone conversion
-- (docs/plan/16 Task T2's ::date vs ::timestamptz::date lesson: a bare
-- `created_at::date` truncates in the SESSION's timezone, which is not
-- guaranteed to be Asia/Jakarta — the AT TIME ZONE conversion below is
-- unambiguous regardless of the querying session's timezone setting).
CREATE VIEW v_report_daily_mutation AS
SELECT
    (t.created_at AT TIME ZONE 'Asia/Jakarta')::date AS report_date,
    t.type     AS tx_type,
    t.currency,
    count(*)        AS tx_count,
    sum(t.amount)   AS total_amount
FROM ledger_transactions t
WHERE t.status = 'posted'
GROUP BY (t.created_at AT TIME ZONE 'Asia/Jakarta')::date, t.type, t.currency;

-- v_report_recon_summary: one row per reconciliation batch (H2) — gateway,
-- report_date, per-match_status counts, and how many items are already
-- resolved via the maker-checker path (resolved_by_adjustment_id set).
CREATE VIEW v_report_recon_summary AS
SELECT
    b.id              AS batch_id,
    b.gateway,
    b.report_date,
    b.source_filename,
    b.status          AS batch_status,
    b.row_count       AS declared_row_count,
    count(i.id)                                                          AS item_count,
    count(*) FILTER (WHERE i.match_status = 'matched')                   AS matched_count,
    count(*) FILTER (WHERE i.match_status = 'missing_internal')          AS missing_internal_count,
    count(*) FILTER (WHERE i.match_status = 'missing_external')          AS missing_external_count,
    count(*) FILTER (WHERE i.match_status = 'amount_mismatch')           AS amount_mismatch_count,
    count(*) FILTER (WHERE i.resolved_by_adjustment_id IS NOT NULL)      AS resolved_count
FROM recon_batches b
LEFT JOIN recon_items i ON i.batch_id = b.id
GROUP BY b.id, b.gateway, b.report_date, b.source_filename, b.status, b.row_count;

GRANT SELECT ON v_report_daily_position, v_report_daily_mutation, v_report_recon_summary
TO app_readonly, app_service;
