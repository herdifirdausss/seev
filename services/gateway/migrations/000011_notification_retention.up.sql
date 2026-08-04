-- C3 retention is deliberately implemented as one bounded SECURITY DEFINER
-- function per policy class.  The application role can operate the delivery
-- tables, but it cannot bypass the A8 hold checks with a direct DELETE.

-- Token erasure keeps the endpoint row and its keyed fingerprint while
-- removing the provider credential after the invalid/revoked grace period.
ALTER TABLE notif_device_endpoints
    ALTER COLUMN token_ciphertext DROP NOT NULL;

CREATE OR REPLACE FUNCTION fn_retention_purge_notification_event_inbox_processed(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started TIMESTAMPTZ := clock_timestamp();
    v_class CONSTANT TEXT := 'gateway.notifications.event_inbox';
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM notif_event_inbox e
        WHERE e.status = 'processed'
          AND e.processed_at < now() - INTERVAL '30 days'
          AND NOT fn_gateway_retention_hold_covers('notif_event_inbox', NULL, e.id::text, e.processed_at);
    ELSE
        WITH eligible AS (
            SELECT e.id
            FROM notif_event_inbox e
            WHERE e.status = 'processed'
              AND e.processed_at < now() - INTERVAL '30 days'
              AND NOT fn_gateway_retention_hold_covers('notif_event_inbox', NULL, e.id::text, e.processed_at)
            ORDER BY e.processed_at, e.id
            LIMIT p_batch_size
            FOR UPDATE OF e SKIP LOCKED
        ), deleted AS (
            DELETE FROM notif_event_inbox e
            USING eligible
            WHERE e.id = eligible.id
            RETURNING e.id
        )
        SELECT count(*) INTO v_affected FROM deleted;
    END IF;

    INSERT INTO gateway_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
    VALUES(p_job_id,v_class,'delete',p_dry_run,v_affected,1,v_started,'ok');
    RETURN v_affected;
END;
$$;

CREATE OR REPLACE FUNCTION fn_retention_purge_notification_event_inbox_failed(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started TIMESTAMPTZ := clock_timestamp();
    v_class CONSTANT TEXT := 'gateway.notifications.event_inbox_failed';
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM notif_event_inbox e
        WHERE e.status = 'failed'
          AND e.updated_at < now() - INTERVAL '90 days'
          AND NOT fn_gateway_retention_hold_covers('notif_event_inbox', NULL, e.id::text, e.updated_at);
    ELSE
        WITH eligible AS (
            SELECT e.id
            FROM notif_event_inbox e
            WHERE e.status = 'failed'
              AND e.updated_at < now() - INTERVAL '90 days'
              AND NOT fn_gateway_retention_hold_covers('notif_event_inbox', NULL, e.id::text, e.updated_at)
            ORDER BY e.updated_at, e.id
            LIMIT p_batch_size
            FOR UPDATE OF e SKIP LOCKED
        ), deleted AS (
            DELETE FROM notif_event_inbox e
            USING eligible
            WHERE e.id = eligible.id
            RETURNING e.id
        )
        SELECT count(*) INTO v_affected FROM deleted;
    END IF;

    INSERT INTO gateway_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
    VALUES(p_job_id,v_class,'delete',p_dry_run,v_affected,1,v_started,'ok');
    RETURN v_affected;
END;
$$;

CREATE OR REPLACE FUNCTION fn_retention_purge_notification_delivery_attempts(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started TIMESTAMPTZ := clock_timestamp();
    v_class CONSTANT TEXT := 'gateway.notifications.delivery_attempts';
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM notif_delivery_attempts a
        WHERE a.finished_at IS NOT NULL
          AND a.finished_at < now() - INTERVAL '90 days'
          AND NOT fn_gateway_retention_hold_covers('notif_delivery_attempts', NULL, a.id::text, a.finished_at);
    ELSE
        WITH eligible AS (
            SELECT a.id
            FROM notif_delivery_attempts a
            WHERE a.finished_at IS NOT NULL
              AND a.finished_at < now() - INTERVAL '90 days'
              AND NOT fn_gateway_retention_hold_covers('notif_delivery_attempts', NULL, a.id::text, a.finished_at)
            ORDER BY a.finished_at, a.id
            LIMIT p_batch_size
            FOR UPDATE OF a SKIP LOCKED
        ), deleted AS (
            DELETE FROM notif_delivery_attempts a
            USING eligible
            WHERE a.id = eligible.id
            RETURNING a.id
        )
        SELECT count(*) INTO v_affected FROM deleted;
    END IF;

    INSERT INTO gateway_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
    VALUES(p_job_id,v_class,'delete',p_dry_run,v_affected,1,v_started,'ok');
    RETURN v_affected;
END;
$$;

CREATE OR REPLACE FUNCTION fn_retention_redact_notification_recipient(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started TIMESTAMPTZ := clock_timestamp();
    v_class CONSTANT TEXT := 'gateway.notifications.recipient_ciphertext';
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM notif_deliveries d
        WHERE d.recipient_ciphertext IS NOT NULL
          AND d.status IN ('delivered','suppressed','dead','cancelled')
          AND COALESCE(d.delivered_at,d.suppressed_at,d.dead_at,d.updated_at) < now() - INTERVAL '30 days'
          AND NOT fn_gateway_retention_hold_covers('notif_deliveries', d.user_id::text, d.id::text,
              COALESCE(d.delivered_at,d.suppressed_at,d.dead_at,d.updated_at));
    ELSE
        WITH eligible AS (
            SELECT d.id
            FROM notif_deliveries d
            WHERE d.recipient_ciphertext IS NOT NULL
              AND d.status IN ('delivered','suppressed','dead','cancelled')
              AND COALESCE(d.delivered_at,d.suppressed_at,d.dead_at,d.updated_at) < now() - INTERVAL '30 days'
              AND NOT fn_gateway_retention_hold_covers('notif_deliveries', d.user_id::text, d.id::text,
                  COALESCE(d.delivered_at,d.suppressed_at,d.dead_at,d.updated_at))
            ORDER BY COALESCE(d.delivered_at,d.suppressed_at,d.dead_at,d.updated_at), d.id
            LIMIT p_batch_size
            FOR UPDATE OF d SKIP LOCKED
        ), redacted AS (
            UPDATE notif_deliveries d
            SET recipient_ciphertext=NULL, recipient_key_version=NULL,
                recipient_fingerprint=NULL, updated_at=now()
            FROM eligible
            WHERE d.id=eligible.id
            RETURNING d.id
        )
        SELECT count(*) INTO v_affected FROM redacted;
    END IF;

    INSERT INTO gateway_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
    VALUES(p_job_id,v_class,'redact',p_dry_run,v_affected,1,v_started,'ok');
    RETURN v_affected;
END;
$$;

CREATE OR REPLACE FUNCTION fn_retention_redact_notification_device_tokens(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started TIMESTAMPTZ := clock_timestamp();
    v_class CONSTANT TEXT := 'gateway.notifications.device_tokens';
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM notif_device_endpoints d
        WHERE d.token_ciphertext IS NOT NULL
          AND d.status IN ('invalid','revoked')
          AND COALESCE(d.revoked_at,d.updated_at) < now() - INTERVAL '30 days'
          AND NOT fn_gateway_retention_hold_covers('notif_device_endpoints', d.user_id::text, d.id::text,
              COALESCE(d.revoked_at,d.updated_at));
    ELSE
        WITH eligible AS (
            SELECT d.id
            FROM notif_device_endpoints d
            WHERE d.token_ciphertext IS NOT NULL
              AND d.status IN ('invalid','revoked')
              AND COALESCE(d.revoked_at,d.updated_at) < now() - INTERVAL '30 days'
              AND NOT fn_gateway_retention_hold_covers('notif_device_endpoints', d.user_id::text, d.id::text,
                  COALESCE(d.revoked_at,d.updated_at))
            ORDER BY COALESCE(d.revoked_at,d.updated_at), d.id
            LIMIT p_batch_size
            FOR UPDATE OF d SKIP LOCKED
        ), redacted AS (
            UPDATE notif_device_endpoints d
            SET token_ciphertext=NULL, updated_at=now()
            FROM eligible
            WHERE d.id=eligible.id
            RETURNING d.id
        )
        SELECT count(*) INTO v_affected FROM redacted;
    END IF;

    INSERT INTO gateway_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
    VALUES(p_job_id,v_class,'redact',p_dry_run,v_affected,1,v_started,'ok');
    RETURN v_affected;
END;
$$;

CREATE OR REPLACE FUNCTION fn_retention_purge_notification_deliveries(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started TIMESTAMPTZ := clock_timestamp();
    v_class CONSTANT TEXT := 'gateway.notifications.deliveries';
    v_ids UUID[];
BEGIN
    SELECT COALESCE(array_agg(d.id), ARRAY[]::UUID[]) INTO v_ids
    FROM (
        SELECT d.id
        FROM notif_deliveries d
        WHERE d.status IN ('delivered','suppressed','dead','cancelled')
          AND COALESCE(d.delivered_at,d.suppressed_at,d.dead_at,d.updated_at) < now() - INTERVAL '180 days'
          AND NOT fn_gateway_retention_hold_covers('notif_deliveries', d.user_id::text, d.id::text,
              COALESCE(d.delivered_at,d.suppressed_at,d.dead_at,d.updated_at))
        ORDER BY COALESCE(d.delivered_at,d.suppressed_at,d.dead_at,d.updated_at), d.id
        LIMIT p_batch_size
        FOR UPDATE SKIP LOCKED
    ) d;
    v_affected := cardinality(v_ids);

    IF NOT p_dry_run AND v_affected > 0 THEN
        UPDATE notif_digest_windows SET delivery_id=NULL, updated_at=now()
        WHERE delivery_id = ANY(v_ids);
        DELETE FROM notif_delivery_attempts WHERE delivery_id = ANY(v_ids);
        DELETE FROM notif_deliveries WHERE id = ANY(v_ids);
    END IF;

    INSERT INTO gateway_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
    VALUES(p_job_id,v_class,'delete',p_dry_run,v_affected,1,v_started,'ok');
    RETURN v_affected;
END;
$$;

CREATE OR REPLACE FUNCTION fn_retention_purge_notification_digest_items(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started TIMESTAMPTZ := clock_timestamp();
    v_class CONSTANT TEXT := 'gateway.notifications.digest_items';
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM notif_digest_items i
        JOIN notif_digest_windows w ON w.id=i.digest_window_id
        WHERE w.status IN ('delivered','suppressed','dead')
          AND w.updated_at < now() - INTERVAL '90 days'
          AND NOT fn_gateway_retention_hold_covers('notif_digest_items', w.user_id::text,
              i.notification_id::text, w.updated_at);
    ELSE
        WITH eligible AS (
            SELECT i.digest_window_id, i.notification_id
            FROM notif_digest_items i
            JOIN notif_digest_windows w ON w.id=i.digest_window_id
            WHERE w.status IN ('delivered','suppressed','dead')
              AND w.updated_at < now() - INTERVAL '90 days'
              AND NOT fn_gateway_retention_hold_covers('notif_digest_items', w.user_id::text,
                  i.notification_id::text, w.updated_at)
            ORDER BY w.updated_at, i.digest_window_id, i.notification_id
            LIMIT p_batch_size
            FOR UPDATE OF i SKIP LOCKED
        ), deleted AS (
            DELETE FROM notif_digest_items i
            USING eligible
            WHERE i.digest_window_id=eligible.digest_window_id
              AND i.notification_id=eligible.notification_id
            RETURNING i.notification_id
        )
        SELECT count(*) INTO v_affected FROM deleted;
    END IF;

    INSERT INTO gateway_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
    VALUES(p_job_id,v_class,'delete',p_dry_run,v_affected,1,v_started,'ok');
    RETURN v_affected;
END;
$$;

CREATE OR REPLACE FUNCTION fn_retention_purge_notification_digest_windows(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started TIMESTAMPTZ := clock_timestamp();
    v_class CONSTANT TEXT := 'gateway.notifications.digest_windows';
    v_ids UUID[];
BEGIN
    SELECT COALESCE(array_agg(w.id), ARRAY[]::UUID[]) INTO v_ids
    FROM (
        SELECT w.id
        FROM notif_digest_windows w
        WHERE w.status IN ('delivered','suppressed','dead')
          AND w.updated_at < now() - INTERVAL '90 days'
          AND NOT fn_gateway_retention_hold_covers('notif_digest_windows', w.user_id::text, w.id::text, w.updated_at)
        ORDER BY w.updated_at, w.id
        LIMIT p_batch_size
        FOR UPDATE SKIP LOCKED
    ) w;
    v_affected := cardinality(v_ids);

    IF NOT p_dry_run AND v_affected > 0 THEN
        DELETE FROM notif_digest_items WHERE digest_window_id = ANY(v_ids);
        DELETE FROM notif_digest_windows WHERE id = ANY(v_ids);
    END IF;

    INSERT INTO gateway_retention_audit(job_id,class,action,dry_run,affected_count,policy_version,started_at,result)
    VALUES(p_job_id,v_class,'delete',p_dry_run,v_affected,1,v_started,'ok');
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_notification_event_inbox_processed(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_notification_event_inbox_failed(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_notification_delivery_attempts(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_redact_notification_recipient(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_redact_notification_device_tokens(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_notification_deliveries(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_notification_digest_items(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_notification_digest_windows(UUID, INT, BOOLEAN) TO app_service;
