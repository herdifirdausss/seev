-- config/data-retention.yaml vendor.callback_inbox: redact raw callback
-- bytes/headers 30 days after a terminal outcome; sanitized correlation
-- columns (vendor, vendor_event_id, external_reference, amount, currency,
-- normalized_status, processing_status, attempts, outcome, timestamps)
-- remain for reconciliation, matching the "allowlisted correlation
-- columns" precedent already used for payin_webhook_events.raw
-- (services/payin/migrations/000009_retention_purge_webhook_raw.up.sql).
--
-- Terminal processing_status values mirror services/vendor-service/internal/callback.go's
-- own Finish(...) call sites ('finalized', 'ignored', 'unmatched') plus
-- 'dead' from the table's CHECK constraint (a dead-lettered row will never
-- be retried further). 'received', 'processing', and 'retry' are
-- explicitly excluded — those rows may still be reprocessed and their raw
-- body is still needed.
--
-- hold_scope: none in policy (a callback row has no subject/resource a
-- legal hold could target), so this does not call
-- fn_vendor_retention_hold_covers — same reasoning already used for
-- assurance.runs.succeeded (services/assurance/migrations/000006_retention_purge_functions.up.sql).
--
-- This redacts row content only — it never deletes the row itself. Row
-- count/index growth on vendor_callback_inbox is accepted, permanent
-- financial-evidence behavior per docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
-- §4.1; long-term row-count growth is B2 (table partitioning) territory,
-- to be revisited if real volume ever approaches that gate, not something
-- a retention function may delete.
CREATE OR REPLACE FUNCTION fn_retention_purge_callback_inbox_raw(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'vendor.callback_inbox';
    v_version  CONSTANT INT  := 1;
    v_redacted_body CONSTANT BYTEA := convert_to('{"redacted":true}', 'UTF8');
BEGIN
    IF p_batch_size < 1 OR p_batch_size > 500 THEN
        RAISE EXCEPTION 'invalid retention batch size';
    END IF;

    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM vendor_callback_inbox c
        WHERE c.processing_status IN ('finalized', 'ignored', 'unmatched', 'dead')
          AND c.updated_at < now() - INTERVAL '30 days'
          AND c.raw_body <> convert_to('{"redacted":true}', 'UTF8');

        INSERT INTO vendor_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'redact', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT c.id
        FROM vendor_callback_inbox c
        WHERE c.processing_status IN ('finalized', 'ignored', 'unmatched', 'dead')
          AND c.updated_at < now() - INTERVAL '30 days'
          AND c.raw_body <> convert_to('{"redacted":true}', 'UTF8')
        ORDER BY c.updated_at
        LIMIT p_batch_size
        FOR UPDATE OF c SKIP LOCKED
    ), redacted AS (
        UPDATE vendor_callback_inbox c
        SET raw_body = v_redacted_body, selected_headers = '{"redacted":true}'::jsonb
        FROM eligible WHERE c.id = eligible.id RETURNING c.id
    )
    SELECT count(*) INTO v_affected FROM redacted;

    INSERT INTO vendor_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'redact', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_callback_inbox_raw(UUID, INT, BOOLEAN) TO app_service;
