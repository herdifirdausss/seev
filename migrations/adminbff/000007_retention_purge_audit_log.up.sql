CREATE OR REPLACE FUNCTION fn_retention_purge_audit_log(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started TIMESTAMPTZ := clock_timestamp();
BEGIN
    IF p_batch_size < 1 OR p_batch_size > 500 THEN
        RAISE EXCEPTION 'invalid retention batch size';
    END IF;
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected FROM audit_log a
        WHERE a.created_at < now() - INTERVAL '365 days'
          AND NOT fn_adminbff_retention_hold_covers('audit_log', a.user_id::text, NULL, a.created_at);
    ELSE
        WITH eligible AS (
            SELECT a.id FROM audit_log a
            WHERE a.created_at < now() - INTERVAL '365 days'
              AND NOT fn_adminbff_retention_hold_covers('audit_log', a.user_id::text, NULL, a.created_at)
            ORDER BY a.created_at, a.id LIMIT p_batch_size FOR UPDATE OF a SKIP LOCKED
        ), deleted AS (
            DELETE FROM audit_log a USING eligible WHERE a.id = eligible.id RETURNING a.id
        ) SELECT count(*) INTO v_affected FROM deleted;
    END IF;
    INSERT INTO adminbff_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, 'adminbff.audit_log', 'delete', p_dry_run, v_affected, 1, v_started, 'ok');
    RETURN v_affected;
END;
$$;
GRANT EXECUTE ON FUNCTION fn_retention_purge_audit_log(UUID, INT, BOOLEAN) TO app_service;
