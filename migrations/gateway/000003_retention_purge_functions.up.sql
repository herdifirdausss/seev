-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T1.7: SECURITY DEFINER retention
-- functions for gateway's notif_notifications classes (config/data-retention.yaml,
-- §4.2 "Read notification" / "Any notification"). Two functions share one
-- physical table with disjoint eligibility windows:
--   - gateway.notifications.read: read_at IS NOT NULL and read more than
--     180 days ago (the primary rule).
--   - gateway.notifications.any: any notification older than 365 days,
--     regardless of read_at — the backstop for rows that are never read.
-- A row eligible under .read is also eligible under .any once it crosses
-- 365 days, but each function only ever deletes rows matching its OWN
-- narrower predicate — running both is safe and produces no double count
-- (each call's WHERE only matches the rows still present).
CREATE OR REPLACE FUNCTION fn_retention_purge_notifications_read(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'gateway.notifications.read';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM notif_notifications n
        WHERE n.read_at IS NOT NULL
          AND n.read_at < now() - INTERVAL '180 days'
          AND NOT fn_gateway_retention_hold_covers('notif_notifications', n.user_id::text, n.id::text, n.read_at);

        INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT n.id
        FROM notif_notifications n
        WHERE n.read_at IS NOT NULL
          AND n.read_at < now() - INTERVAL '180 days'
          AND NOT fn_gateway_retention_hold_covers('notif_notifications', n.user_id::text, n.id::text, n.read_at)
        ORDER BY n.read_at
        LIMIT p_batch_size
        FOR UPDATE OF n SKIP LOCKED
    ), deleted AS (
        DELETE FROM notif_notifications n USING eligible WHERE n.id = eligible.id RETURNING n.id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

CREATE OR REPLACE FUNCTION fn_retention_purge_notifications_any(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'gateway.notifications.any';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM notif_notifications n
        WHERE n.created_at < now() - INTERVAL '365 days'
          AND NOT fn_gateway_retention_hold_covers('notif_notifications', n.user_id::text, n.id::text, n.created_at);

        INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT n.id
        FROM notif_notifications n
        WHERE n.created_at < now() - INTERVAL '365 days'
          AND NOT fn_gateway_retention_hold_covers('notif_notifications', n.user_id::text, n.id::text, n.created_at)
        ORDER BY n.created_at
        LIMIT p_batch_size
        FOR UPDATE OF n SKIP LOCKED
    ), deleted AS (
        DELETE FROM notif_notifications n USING eligible WHERE n.id = eligible.id RETURNING n.id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_notifications_read(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_notifications_any(UUID, INT, BOOLEAN) TO app_service;
