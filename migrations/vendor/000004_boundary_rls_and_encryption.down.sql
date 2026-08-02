DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'vendor_app') THEN
        DROP POLICY IF EXISTS pol_vendor_app ON vendor_callback_inbox;
        DROP POLICY IF EXISTS pol_vendor_app ON vendor_outbound_attempts;
    END IF;
END;
$$;

ALTER TABLE vendor_callback_inbox NO FORCE ROW LEVEL SECURITY;
ALTER TABLE vendor_callback_inbox DISABLE ROW LEVEL SECURITY;
ALTER TABLE vendor_outbound_attempts NO FORCE ROW LEVEL SECURITY;
ALTER TABLE vendor_outbound_attempts DISABLE ROW LEVEL SECURITY;

-- Restores the pre-migration redaction mechanism. Ciphertext data (and any
-- plaintext lost on the up migration's DROP COLUMN) cannot be recovered —
-- same accepted data-loss-on-rollback precedent as
-- migrations/ledger/000035_chargeback_disputes.down.sql.
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

ALTER TABLE vendor_callback_inbox
    ADD COLUMN raw_body BYTEA NOT NULL DEFAULT '',
    ADD COLUMN selected_headers JSONB NOT NULL DEFAULT '{}'::jsonb,
    DROP COLUMN raw_body_ciphertext,
    DROP COLUMN raw_body_key_version,
    DROP COLUMN selected_headers_ciphertext,
    DROP COLUMN selected_headers_key_version;
