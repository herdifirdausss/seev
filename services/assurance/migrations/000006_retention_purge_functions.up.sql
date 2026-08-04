-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T1.7: SECURITY DEFINER retention
-- functions for assurance's runs.succeeded and alert_deliveries classes
-- (config/data-retention.yaml, §4.2 "Assurance successful run" /
-- "failed alert delivery"). Both entries have hold_scope: none (T0's own
-- classification — a pipeline run or a fire-and-forget alert delivery has
-- no subject/resource a hold could ever target), so neither function calls
-- fn_assurance_retention_hold_covers, unlike every hold_scope: subject/
-- resource function elsewhere in this migration set.
--
-- assurance.runs.failed is deliberately NOT covered here: the policy's own
-- note requires "an incident/audit summary to already exist" before a
-- failed run may be purged, and assurance_runs.error_code/error_message
-- are the run row's OWN summary — nothing else in this codebase persists
-- an independent incident record before this row would be deleted, which
-- would violate §4.3's "its successor/audit summary has not been
-- persisted" never-purge condition. Deferred until that mechanism exists.
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

-- Only 'delivered' and 'failed' are terminal (assurance_alert_deliveries
-- has no explicit CHECK-level terminal marker beyond status itself) —
-- 'pending' rows are always excluded regardless of how stale
-- next_attempt_at looks, per §4.3's non-terminal never-purge condition.
CREATE OR REPLACE FUNCTION fn_retention_purge_alert_deliveries(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'assurance.alert_deliveries';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM assurance_alert_deliveries d
        WHERE d.status IN ('delivered', 'failed')
          AND COALESCE(d.delivered_at, d.next_attempt_at) < now() - INTERVAL '180 days';

        INSERT INTO assurance_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT d.id
        FROM assurance_alert_deliveries d
        WHERE d.status IN ('delivered', 'failed')
          AND COALESCE(d.delivered_at, d.next_attempt_at) < now() - INTERVAL '180 days'
        ORDER BY COALESCE(d.delivered_at, d.next_attempt_at)
        LIMIT p_batch_size
        FOR UPDATE OF d SKIP LOCKED
    ), deleted AS (
        DELETE FROM assurance_alert_deliveries d USING eligible WHERE d.id = eligible.id RETURNING d.id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO assurance_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_runs_succeeded(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_alert_deliveries(UUID, INT, BOOLEAN) TO app_service;
