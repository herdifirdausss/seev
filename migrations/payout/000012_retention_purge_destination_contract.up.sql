-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md "A8 T2.5b": migrations/payout/000011
-- dropped payout_requests.destination — this function (migrations/payout/000010)
-- wrote a {"redacted":true} marker into that column on every redaction and
-- used "destination != marker" as part of its own idempotency guard. Both
-- are gone now: redaction simply NULLs destination_ciphertext/
-- destination_key_version (and clears error_message as before);
-- "destination_ciphertext IS NOT NULL OR error_message IS NOT NULL" alone
-- is sufficient to select rows still needing this pass.
CREATE OR REPLACE FUNCTION fn_retention_purge_requests_destination_and_error(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'payout.requests.destination_and_error';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM payout_requests r
        WHERE r.status IN ('settled', 'failed', 'cancelled', 'rejected')
          AND r.updated_at < now() - INTERVAL '30 days'
          AND (r.destination_ciphertext IS NOT NULL OR r.error_message IS NOT NULL)
          AND NOT fn_payout_retention_hold_covers('payout_requests', r.user_id::text, r.id::text, r.updated_at);

        INSERT INTO payout_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'redact', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT r.id
        FROM payout_requests r
        WHERE r.status IN ('settled', 'failed', 'cancelled', 'rejected')
          AND r.updated_at < now() - INTERVAL '30 days'
          AND (r.destination_ciphertext IS NOT NULL OR r.error_message IS NOT NULL)
          AND NOT fn_payout_retention_hold_covers('payout_requests', r.user_id::text, r.id::text, r.updated_at)
        ORDER BY r.updated_at
        LIMIT p_batch_size
        FOR UPDATE OF r SKIP LOCKED
    ), redacted AS (
        UPDATE payout_requests r
        SET destination_ciphertext = NULL, destination_key_version = NULL, error_message = NULL
        FROM eligible WHERE r.id = eligible.id RETURNING r.id
    )
    SELECT count(*) INTO v_affected FROM redacted;

    INSERT INTO payout_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'redact', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_requests_destination_and_error(UUID, INT, BOOLEAN) TO app_service;
