-- Schema audit finding: fn_retention_purge_runs_succeeded (000006) could
-- attempt to hard-delete an assurance_runs row still referenced by
-- assurance_cursors.updated_by_run_id (FK, no ON DELETE clause) — every
-- other multi-table purge function in this codebase guards this with a
-- NOT EXISTS check (e.g. assurance_findings vs alert_deliveries in
-- 000007_retention_remaining.up.sql), this one didn't. Without the guard,
-- a source (payin/payout/ledger) that stops running assurance scans for
-- 90+ days leaves its cursor pointing at a run that's now eligible for
-- deletion — the next retention batch's DELETE fails on the FK constraint
-- and the whole job errors every cycle until fixed by hand. policy_version
-- bumped 1->2 to reflect the eligibility-query change.
CREATE OR REPLACE FUNCTION fn_retention_purge_runs_succeeded(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'assurance.runs.succeeded';
    v_version  CONSTANT INT  := 2;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM assurance_runs r
        WHERE r.status = 'succeeded'
          AND r.finished_at IS NOT NULL
          AND r.finished_at < now() - INTERVAL '90 days'
          AND NOT EXISTS (SELECT 1 FROM assurance_cursors c WHERE c.updated_by_run_id = r.id);

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
          AND NOT EXISTS (SELECT 1 FROM assurance_cursors c WHERE c.updated_by_run_id = r.id)
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

-- assurance_alert_deliveries.finding_id (FK) had no supporting index —
-- fn_retention_purge_findings_resolved's NOT EXISTS check against this
-- table (000007_retention_remaining.up.sql) forces a sequential scan per
-- candidate finding without it (schema audit finding).
CREATE INDEX idx_assurance_alert_deliveries_finding ON assurance_alert_deliveries(finding_id);
