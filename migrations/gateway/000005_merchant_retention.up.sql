-- docs/roadmap/active/57-c1-merchant-b2b-api.md T2 "Add retention jobs for
-- idempotency and delivery evidence" — same SECURITY DEFINER
-- purge-function-per-class pattern as every other owner (K4/K5 shared
-- convention from docs/roadmap/archive/51). config/data-retention.yaml carries the
-- matching classification entries.

CREATE OR REPLACE FUNCTION fn_retention_purge_merchant_idempotency_records(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'gateway.merchant.idempotency_records';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM merchant_idempotency_records
        WHERE expires_at < now() - INTERVAL '24 hours'
          AND NOT fn_gateway_retention_hold_covers('merchant_idempotency_records', tenant_id::text, id::text, expires_at);

        INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT id
        FROM merchant_idempotency_records
        WHERE expires_at < now() - INTERVAL '24 hours'
          AND NOT fn_gateway_retention_hold_covers('merchant_idempotency_records', tenant_id::text, id::text, expires_at)
        ORDER BY expires_at
        LIMIT p_batch_size
        FOR UPDATE SKIP LOCKED
    ), deleted AS (
        DELETE FROM merchant_idempotency_records WHERE id IN (SELECT id FROM eligible) RETURNING id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

CREATE OR REPLACE FUNCTION fn_retention_purge_merchant_api_keys_revoked(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'gateway.merchant.api_keys_revoked';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM merchant_api_keys
        WHERE status = 'revoked' AND revoked_at < now() - INTERVAL '90 days'
          AND NOT fn_gateway_retention_hold_covers('merchant_api_keys', tenant_id::text, id::text, revoked_at);

        INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT id
        FROM merchant_api_keys
        WHERE status = 'revoked' AND revoked_at < now() - INTERVAL '90 days'
          AND NOT fn_gateway_retention_hold_covers('merchant_api_keys', tenant_id::text, id::text, revoked_at)
        ORDER BY revoked_at
        LIMIT p_batch_size
        FOR UPDATE SKIP LOCKED
    ), scopes_deleted AS (
        DELETE FROM merchant_api_key_scopes WHERE key_id IN (SELECT id FROM eligible)
    ), deleted AS (
        DELETE FROM merchant_api_keys WHERE id IN (SELECT id FROM eligible) RETURNING id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

CREATE OR REPLACE FUNCTION fn_retention_purge_merchant_event_inbox(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'gateway.merchant.event_inbox';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM merchant_event_inbox
        WHERE processed_at IS NOT NULL AND processed_at < now() - INTERVAL '30 days';

        INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT event_id
        FROM merchant_event_inbox
        WHERE processed_at IS NOT NULL AND processed_at < now() - INTERVAL '30 days'
        ORDER BY processed_at
        LIMIT p_batch_size
        FOR UPDATE SKIP LOCKED
    ), deleted AS (
        DELETE FROM merchant_event_inbox WHERE event_id IN (SELECT event_id FROM eligible) RETURNING event_id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

-- Deliveries purge FIRST (webhook_attempts cascade-delete with their
-- parent delivery); events purge only once no delivery still references
-- them, so this pair is safe to run in either relative order without a
-- foreign-key violation.
CREATE OR REPLACE FUNCTION fn_retention_purge_merchant_webhook_deliveries(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'gateway.merchant.webhook_deliveries';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM merchant_webhook_deliveries
        WHERE status IN ('delivered', 'dead') AND updated_at < now() - INTERVAL '90 days'
          AND NOT fn_gateway_retention_hold_covers('merchant_webhook_deliveries', tenant_id::text, id::text, updated_at);

        INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT id
        FROM merchant_webhook_deliveries
        WHERE status IN ('delivered', 'dead') AND updated_at < now() - INTERVAL '90 days'
          AND NOT fn_gateway_retention_hold_covers('merchant_webhook_deliveries', tenant_id::text, id::text, updated_at)
        ORDER BY updated_at
        LIMIT p_batch_size
        FOR UPDATE SKIP LOCKED
    ), deleted AS (
        DELETE FROM merchant_webhook_deliveries WHERE id IN (SELECT id FROM eligible) RETURNING id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

CREATE OR REPLACE FUNCTION fn_retention_purge_merchant_webhook_events(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'gateway.merchant.webhook_events';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM merchant_webhook_events e
        WHERE e.created_at < now() - INTERVAL '90 days'
          AND NOT EXISTS (SELECT 1 FROM merchant_webhook_deliveries d WHERE d.event_id = e.id)
          AND NOT fn_gateway_retention_hold_covers('merchant_webhook_events', e.tenant_id::text, e.id::text, e.created_at);

        INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT e.id
        FROM merchant_webhook_events e
        WHERE e.created_at < now() - INTERVAL '90 days'
          AND NOT EXISTS (SELECT 1 FROM merchant_webhook_deliveries d WHERE d.event_id = e.id)
          AND NOT fn_gateway_retention_hold_covers('merchant_webhook_events', e.tenant_id::text, e.id::text, e.created_at)
        ORDER BY e.created_at
        LIMIT p_batch_size
        FOR UPDATE OF e SKIP LOCKED
    ), deleted AS (
        DELETE FROM merchant_webhook_events WHERE id IN (SELECT id FROM eligible) RETURNING id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO gateway_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_merchant_idempotency_records(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_merchant_api_keys_revoked(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_merchant_event_inbox(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_merchant_webhook_deliveries(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_merchant_webhook_events(UUID, INT, BOOLEAN) TO app_service;
