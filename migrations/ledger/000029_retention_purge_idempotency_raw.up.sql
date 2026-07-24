-- docs/roadmap/active/51-a8-data-lifecycle-privacy.md T3 (K7, work item 5): SECURITY DEFINER
-- redact function for ledger.transactions.idempotency_raw
-- (config/data-retention.yaml) — declared back in T0, implemented here now
-- that T3's digest/version/conflict_fingerprint columns exist (migration
-- 000028) and can be backfilled first.
--
-- The idempotency_key_digest IS NOT NULL guard is load-bearing, not
-- defensive: this function must NEVER null a row's raw key/scope before a
-- permanent digest already exists to carry the deduplication invariant
-- forward — that would silently and irreversibly disable dedup for that
-- row. hold_scope: none in the policy (no hold check) — unlike every
-- other redact/delete class in this codebase, there is no legitimate
-- reason to EVER hold a row back from this specific redaction once its
-- digest is backfilled: it changes no financial data, only removes an
-- already-superseded raw value.
CREATE OR REPLACE FUNCTION fn_retention_purge_transactions_idempotency_raw(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'ledger.transactions.idempotency_raw';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM ledger_transactions t
        WHERE t.status IN ('posted', 'failed', 'reversed')
          AND t.updated_at < now() - INTERVAL '30 days'
          AND t.idempotency_key IS NOT NULL
          AND t.idempotency_key_digest IS NOT NULL;

        INSERT INTO ledger_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'redact', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT t.id
        FROM ledger_transactions t
        WHERE t.status IN ('posted', 'failed', 'reversed')
          AND t.updated_at < now() - INTERVAL '30 days'
          AND t.idempotency_key IS NOT NULL
          AND t.idempotency_key_digest IS NOT NULL
        ORDER BY t.updated_at
        LIMIT p_batch_size
        FOR UPDATE OF t SKIP LOCKED
    ), redacted AS (
        UPDATE ledger_transactions t
        SET idempotency_key = NULL, idempotency_scope = NULL
        FROM eligible WHERE t.id = eligible.id RETURNING t.id
    )
    SELECT count(*) INTO v_affected FROM redacted;

    INSERT INTO ledger_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'redact', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_transactions_idempotency_raw(UUID, INT, BOOLEAN) TO app_service;
