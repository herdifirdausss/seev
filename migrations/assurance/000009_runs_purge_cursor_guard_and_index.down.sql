DROP INDEX IF EXISTS idx_assurance_alert_deliveries_finding;

CREATE OR REPLACE FUNCTION fn_retention_purge_runs_succeeded(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'assurance.runs.succeeded';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM assurance_runs r
        WHERE r.status = 'succeeded'
          AND r.finished_at IS NOT NULL
          AND r.finished_at < now() - INTERVAL '90 days';

        INSERT INTO assurance_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT r.id
        FROM assurance_runs r
        WHERE r.status = 'succeeded'
          AND r.finished_at IS NOT NULL
          AND r.finished_at < now() - INTERVAL '90 days'
        ORDER BY r.finished_at
        LIMIT p_batch_size
        FOR UPDATE OF r SKIP LOCKED
    ), deleted AS (
        DELETE FROM assurance_runs r USING eligible WHERE r.id = eligible.id RETURNING r.id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO assurance_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;
