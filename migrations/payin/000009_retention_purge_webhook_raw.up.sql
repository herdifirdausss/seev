-- docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.6: SECURITY DEFINER redact
-- function for payin.webhook_events.raw (config/data-retention.yaml) — the
-- class was already declared in T0's policy but never implemented (T1's
-- own scope explicitly listed only its DELETE classes). Redacts BOTH the
-- plaintext `raw` column and the T2.4 `raw_ciphertext`/`raw_key_version`
-- columns without ever decrypting: nulling ciphertext needs no key at all,
-- it is exactly as safe/cheap as nulling plaintext. vendor/vendor_event_id/
-- external_ref/amount/currency/status remain (the policy's own
-- "allowlisted correlation columns"). `raw` is NOT NULL, so redaction
-- writes a small marker object instead of NULL — same convention as every
-- other NOT NULL sensitive column this task redacts.
CREATE OR REPLACE FUNCTION fn_retention_purge_webhook_events_raw(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'payin.webhook_events.raw';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM payin_webhook_events e
        WHERE e.status IN ('posted', 'failed', 'blocked')
          AND e.updated_at < now() - INTERVAL '30 days'
          AND e.raw_ciphertext IS NOT NULL
          AND e.raw != '{"redacted":true}'::jsonb
          AND NOT fn_payin_retention_hold_covers('payin_webhook_events', e.user_id::text, e.id::text, e.updated_at);

        INSERT INTO payin_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'redact', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT e.id
        FROM payin_webhook_events e
        WHERE e.status IN ('posted', 'failed', 'blocked')
          AND e.updated_at < now() - INTERVAL '30 days'
          AND e.raw_ciphertext IS NOT NULL
          AND e.raw != '{"redacted":true}'::jsonb
          AND NOT fn_payin_retention_hold_covers('payin_webhook_events', e.user_id::text, e.id::text, e.updated_at)
        ORDER BY e.updated_at
        LIMIT p_batch_size
        FOR UPDATE OF e SKIP LOCKED
    ), redacted AS (
        UPDATE payin_webhook_events e
        SET raw = '{"redacted":true}'::jsonb, raw_ciphertext = NULL, raw_key_version = NULL
        FROM eligible WHERE e.id = eligible.id RETURNING e.id
    )
    SELECT count(*) INTO v_affected FROM redacted;

    INSERT INTO payin_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'redact', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_webhook_events_raw(UUID, INT, BOOLEAN) TO app_service;
