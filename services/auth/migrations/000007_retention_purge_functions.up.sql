-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T1: SECURITY DEFINER retention
-- function for auth's refresh_tokens class (config/data-retention.yaml).
-- Fixed (p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN) RETURNS INT
-- signature (internal/platform/lifecycle/retention/worker.Class), excludes rows an auth_retention_holds
-- row covers via fn_auth_retention_hold_covers (000006), and writes its own
-- auth_retention_audit row in the same transaction (K4).

-- ── auth_refresh_tokens — docs/roadmap/archive/51 §4.2, 30d after revoke/expiry ─────
CREATE OR REPLACE FUNCTION fn_retention_purge_refresh_tokens(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'auth.refresh_tokens';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM auth_refresh_tokens t
        WHERE (
                (t.revoked_at IS NOT NULL AND t.revoked_at < now() - INTERVAL '30 days') OR
                (t.revoked_at IS NULL AND t.expires_at < now() - INTERVAL '30 days')
              )
          AND NOT fn_auth_retention_hold_covers('auth_refresh_tokens', t.user_id::text, t.id::text, COALESCE(t.revoked_at, t.expires_at));

        INSERT INTO auth_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT t.id
        FROM auth_refresh_tokens t
        WHERE (
                (t.revoked_at IS NOT NULL AND t.revoked_at < now() - INTERVAL '30 days') OR
                (t.revoked_at IS NULL AND t.expires_at < now() - INTERVAL '30 days')
              )
          AND NOT fn_auth_retention_hold_covers('auth_refresh_tokens', t.user_id::text, t.id::text, COALESCE(t.revoked_at, t.expires_at))
        ORDER BY COALESCE(t.revoked_at, t.expires_at)
        LIMIT p_batch_size
        FOR UPDATE OF t SKIP LOCKED
    ), deleted AS (
        DELETE FROM auth_refresh_tokens t USING eligible WHERE t.id = eligible.id RETURNING t.id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO auth_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_refresh_tokens(UUID, INT, BOOLEAN) TO app_service;
