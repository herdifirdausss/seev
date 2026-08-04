-- Restores the pre-contract function bodies (services/ledger/migrations/000027) —
-- structural rollback only; these reference recon_batches.source_filename/
-- recon_items.raw, which only exist again once services/ledger/migrations/000030's
-- own down runs too.
CREATE OR REPLACE FUNCTION fn_retention_purge_recon_batches(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'ledger.recon_batches';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM recon_batches b
        WHERE b.status IN ('completed', 'failed')
          AND b.created_at < now() - INTERVAL '90 days'
          AND b.source_filename != 'REDACTED'
          AND NOT fn_ledger_retention_hold_covers('recon_batches', NULL, b.id::text, b.created_at);

        INSERT INTO ledger_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'redact', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT b.id
        FROM recon_batches b
        WHERE b.status IN ('completed', 'failed')
          AND b.created_at < now() - INTERVAL '90 days'
          AND b.source_filename != 'REDACTED'
          AND NOT fn_ledger_retention_hold_covers('recon_batches', NULL, b.id::text, b.created_at)
        ORDER BY b.created_at
        LIMIT p_batch_size
        FOR UPDATE OF b SKIP LOCKED
    ), redacted AS (
        UPDATE recon_batches b
        SET source_filename = 'REDACTED', source_filename_ciphertext = NULL, source_filename_key_version = NULL
        FROM eligible WHERE b.id = eligible.id RETURNING b.id
    )
    SELECT count(*) INTO v_affected FROM redacted;

    INSERT INTO ledger_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'redact', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

CREATE OR REPLACE FUNCTION fn_retention_purge_recon_items(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'ledger.recon_items';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM recon_items i
        JOIN recon_batches b ON b.id = i.batch_id
        WHERE b.status IN ('completed', 'failed')
          AND b.created_at < now() - INTERVAL '90 days'
          AND (i.raw IS NOT NULL OR i.raw_ciphertext IS NOT NULL)
          AND NOT fn_ledger_retention_hold_covers('recon_items', NULL, i.id::text, b.created_at);

        INSERT INTO ledger_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'redact', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT i.id
        FROM recon_items i
        JOIN recon_batches b ON b.id = i.batch_id
        WHERE b.status IN ('completed', 'failed')
          AND b.created_at < now() - INTERVAL '90 days'
          AND (i.raw IS NOT NULL OR i.raw_ciphertext IS NOT NULL)
          AND NOT fn_ledger_retention_hold_covers('recon_items', NULL, i.id::text, b.created_at)
        ORDER BY b.created_at
        LIMIT p_batch_size
        FOR UPDATE OF i SKIP LOCKED
    ), redacted AS (
        UPDATE recon_items i
        SET raw = NULL, raw_ciphertext = NULL, raw_key_version = NULL
        FROM eligible WHERE i.id = eligible.id RETURNING i.id
    )
    SELECT count(*) INTO v_affected FROM redacted;

    INSERT INTO ledger_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'redact', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_recon_batches(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_recon_items(UUID, INT, BOOLEAN) TO app_service;
