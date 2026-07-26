-- Structural rollback only — historical plaintext for already-redacted
-- rows (ciphertext already NULL) is not recoverable; a genuine rollback
-- requires restoring from a pre-migration backup.
DROP VIEW v_report_recon_summary;

ALTER TABLE recon_batches ADD COLUMN source_filename TEXT NOT NULL DEFAULT 'UNKNOWN';
ALTER TABLE recon_items ADD COLUMN raw JSONB;

CREATE VIEW v_report_recon_summary AS
SELECT
    b.id              AS batch_id,
    b.gateway,
    b.report_date,
    b.source_filename,
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
GROUP BY b.id, b.gateway, b.report_date, b.source_filename, b.status, b.row_count;

GRANT SELECT ON v_report_recon_summary TO app_readonly, app_service;
