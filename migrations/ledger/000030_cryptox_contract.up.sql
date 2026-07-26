-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md "A8 T2.5b" (contract migration):
-- drops the plaintext recon_batches.source_filename and recon_items.raw
-- columns now that the expand-phase backfill has baked. Deliberately does
-- NOT make either ciphertext column NOT NULL — both have their own T2.6
-- retention-redaction function (migrations/ledger/000027) that
-- legitimately NULLs the ciphertext after 90 days, and recon_items.raw
-- was already nullable independent of retention (a synthesized
-- missing_external row never has one) — both are real "nothing to
-- decrypt" states, not bugs. Application code (recon_repository.go) now
-- treats a NULL source_filename_ciphertext as the "REDACTED" marker the
-- retention function used to write into the plaintext column directly,
-- and a NULL raw_ciphertext as simply "no raw for this row" — the exact
-- same meaning the pre-contract dual-read path already gave it.
-- The reporting view used to project the plaintext source filename. Preserve
-- its public column for CSV/API compatibility, but make it a fixed redaction
-- marker before removing the underlying value.
DROP VIEW v_report_recon_summary;

ALTER TABLE recon_batches DROP COLUMN source_filename;
ALTER TABLE recon_items DROP COLUMN raw;

CREATE VIEW v_report_recon_summary AS
SELECT
    b.id              AS batch_id,
    b.gateway,
    b.report_date,
    'REDACTED'::TEXT  AS source_filename,
    b.status          AS batch_status,
    b.row_count       AS declared_row_count,
    count(i.id)                                                     AS item_count,
    count(*) FILTER (WHERE i.match_status = 'matched')              AS matched_count,
    count(*) FILTER (WHERE i.match_status = 'missing_internal')     AS missing_internal_count,
    count(*) FILTER (WHERE i.match_status = 'missing_external')     AS missing_external_count,
    count(*) FILTER (WHERE i.match_status = 'amount_mismatch')      AS amount_mismatch_count,
    count(*) FILTER (WHERE i.resolved_by_adjustment_id IS NOT NULL) AS resolved_count
FROM recon_batches b
LEFT JOIN recon_items i ON i.batch_id = b.id
GROUP BY b.id, b.gateway, b.report_date, b.status, b.row_count;

GRANT SELECT ON v_report_recon_summary TO app_readonly, app_service;
