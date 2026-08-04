-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T1.7: SECURITY DEFINER retention
-- function for auth's kyc_apply_retries.succeeded class (config/data-retention.yaml,
-- §4.2 "Successful KYC apply retry").
--
-- kyc_apply_retries.dead is deliberately NOT covered here: the policy's own
-- note requires "an audit summary to already exist" (§4.2/§4.3) before a
-- dead retry may be purged, and no such summary is persisted anywhere in
-- this codebase today (services/auth/internal/worker/retry.go only sets
-- status='dead' plus a structured log line — logs are not a queryable
-- persisted summary). Purging dead rows now would violate §4.3's "its
-- successor/audit summary has not been persisted" never-purge condition.
-- Deferred until that summary mechanism exists.
CREATE OR REPLACE FUNCTION fn_retention_purge_kyc_apply_retries_succeeded(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'auth.kyc_apply_retries.succeeded';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM kyc_apply_retries r
        WHERE r.status = 'succeeded'
          AND r.updated_at < now() - INTERVAL '90 days'
          AND NOT fn_auth_retention_hold_covers('kyc_apply_retries', r.user_id::text, r.id::text, r.updated_at);

        INSERT INTO auth_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT r.id
        FROM kyc_apply_retries r
        WHERE r.status = 'succeeded'
          AND r.updated_at < now() - INTERVAL '90 days'
          AND NOT fn_auth_retention_hold_covers('kyc_apply_retries', r.user_id::text, r.id::text, r.updated_at)
        ORDER BY r.updated_at
        LIMIT p_batch_size
        FOR UPDATE OF r SKIP LOCKED
    ), deleted AS (
        DELETE FROM kyc_apply_retries r USING eligible WHERE r.id = eligible.id RETURNING r.id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO auth_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_kyc_apply_retries_succeeded(UUID, INT, BOOLEAN) TO app_service;
