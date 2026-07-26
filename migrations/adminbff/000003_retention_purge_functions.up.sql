-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T1: SECURITY DEFINER retention
-- function for adminbff's sessions class (config/data-retention.yaml).
-- Fixed (p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN) RETURNS INT
-- signature (pkg/retentionworker.Class), excludes rows an adminbff_retention_holds
-- row covers via fn_adminbff_retention_hold_covers (000002), and writes its
-- own adminbff_retention_audit row in the same transaction (K4).
--
-- Replaces the old application-level CleanupSessions (`DELETE FROM sessions
-- WHERE ...`, deleted on the session's own expiry moment) with the policy's
-- actual rule (7 days after expiry). A real, load-bearing reason this
-- SECURITY DEFINER approach is required, not optional: adminbff_app was
-- only ever granted SELECT/INSERT/UPDATE on `sessions`
-- (migrations/adminbff/000001_core.up.sql) — it has never had DELETE, so
-- the old CleanupSessions call has been failing with "permission denied
-- for table sessions" in every real deployment path this whole time
-- (confirmed live during this task). A SECURITY DEFINER function runs with
-- its owning role's privileges regardless of the caller's own grants,
-- which is exactly what fixes this, not a side effect of it.
CREATE OR REPLACE FUNCTION fn_retention_purge_sessions(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'adminbff.sessions';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM sessions s
        WHERE GREATEST(s.expires_at, s.absolute_expires_at) < now() - INTERVAL '7 days'
          AND NOT fn_adminbff_retention_hold_covers('sessions', s.user_id::text, s.id, GREATEST(s.expires_at, s.absolute_expires_at));

        INSERT INTO adminbff_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT s.id
        FROM sessions s
        WHERE GREATEST(s.expires_at, s.absolute_expires_at) < now() - INTERVAL '7 days'
          AND NOT fn_adminbff_retention_hold_covers('sessions', s.user_id::text, s.id, GREATEST(s.expires_at, s.absolute_expires_at))
        ORDER BY GREATEST(s.expires_at, s.absolute_expires_at)
        LIMIT p_batch_size
        FOR UPDATE OF s SKIP LOCKED
    ), deleted AS (
        DELETE FROM sessions s USING eligible WHERE s.id = eligible.id RETURNING s.id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO adminbff_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_sessions(UUID, INT, BOOLEAN) TO app_service;
