-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T1: SECURITY DEFINER retention
-- functions for ledger's three explicitly-named T1 classes
-- (config/data-retention.yaml): fee_quotes.unconsumed, fee_quotes.consumed
-- (K8 proof-aware), and outbox_events.published. Every function shares the
-- fixed (p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN) RETURNS INT
-- signature internal/retentionworker.Class documents, excludes rows a
-- retention_holds row covers via fn_ledger_retention_hold_covers (000024), and
-- writes its own ledger_retention_audit row in the same transaction (K4).

-- ── fee_quotes.unconsumed — docs/roadmap/archive/51 §4.2, 24h after expiry ──────────
CREATE OR REPLACE FUNCTION fn_retention_purge_fee_quotes_unconsumed(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'ledger.fee_quotes.unconsumed';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM fee_quotes fq
        WHERE fq.consumed_at IS NULL
          AND fq.expires_at < now() - INTERVAL '24 hours'
          AND NOT fn_ledger_retention_hold_covers('fee_quotes', fq.user_id::text, fq.id::text, fq.expires_at);

        INSERT INTO ledger_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT fq.id
        FROM fee_quotes fq
        WHERE fq.consumed_at IS NULL
          AND fq.expires_at < now() - INTERVAL '24 hours'
          AND NOT fn_ledger_retention_hold_covers('fee_quotes', fq.user_id::text, fq.id::text, fq.expires_at)
        ORDER BY fq.expires_at
        LIMIT p_batch_size
        FOR UPDATE OF fq SKIP LOCKED
    ), deleted AS (
        DELETE FROM fee_quotes fq USING eligible WHERE fq.id = eligible.id RETURNING fq.id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO ledger_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

-- ── fee_quotes.consumed — docs/roadmap/archive/51 §4.2/K8, 365d, proof-aware ────────
-- K8 requires consumed_by_ref to point to the expected transaction/payout,
-- with booked-fee proof matching, before an old consumed quote is
-- eligible. consumed_by_ref is 'tx:<uuid>' | 'payout:<uuid>'
-- (services/ledger/internal/feepolicy/quote.go). A 'tx:' ref is fully verifiable
-- here: the referenced ledger_transactions row must exist, be posted
-- (terminal), and its booked fee (the same SUM(ledger_entries.amount)
-- WHERE account.type='fee' query services/ledger/assurance.go's own
-- bookedFee already uses) must equal fq.fee_amount. A 'payout:' ref is
-- NOT verifiable from this database alone — payout_requests lives in a
-- separate service database, and Postgres has no cross-database join here.
-- Rather than fabricate a check that can't actually prove anything, this
-- function's eligibility is scoped to 'tx:' refs only; payout-consumed
-- quotes are an explicit, documented T1 follow-up (see this task's Result
-- section), never silently purged on a weaker assumption.
CREATE OR REPLACE FUNCTION fn_retention_purge_fee_quotes_consumed(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'ledger.fee_quotes.consumed';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM fee_quotes fq
        JOIN ledger_transactions lt ON lt.id = substring(fq.consumed_by_ref FROM 4)::uuid
        WHERE fq.consumed_at IS NOT NULL
          AND fq.consumed_at < now() - INTERVAL '365 days'
          AND fq.consumed_by_ref LIKE 'tx:%'
          AND lt.status = 'posted'
          AND fq.fee_amount = COALESCE((
                SELECT SUM(e.amount) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
                WHERE e.transaction_id = lt.id AND a.type = 'fee'
              ), 0)
          AND NOT fn_ledger_retention_hold_covers('fee_quotes', fq.user_id::text, fq.id::text, fq.consumed_at);

        INSERT INTO ledger_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT fq.id
        FROM fee_quotes fq
        JOIN ledger_transactions lt ON lt.id = substring(fq.consumed_by_ref FROM 4)::uuid
        WHERE fq.consumed_at IS NOT NULL
          AND fq.consumed_at < now() - INTERVAL '365 days'
          AND fq.consumed_by_ref LIKE 'tx:%'
          AND lt.status = 'posted'
          AND fq.fee_amount = COALESCE((
                SELECT SUM(e.amount) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
                WHERE e.transaction_id = lt.id AND a.type = 'fee'
              ), 0)
          AND NOT fn_ledger_retention_hold_covers('fee_quotes', fq.user_id::text, fq.id::text, fq.consumed_at)
        ORDER BY fq.consumed_at
        LIMIT p_batch_size
        FOR UPDATE OF fq SKIP LOCKED
    ), deleted AS (
        DELETE FROM fee_quotes fq USING eligible WHERE fq.id = eligible.id RETURNING fq.id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO ledger_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

-- ── outbox_events.published — docs/roadmap/archive/51 §4.2, 30d after publish ───────
-- No user_id column exists on outbox_events itself (docs/roadmap/archive/51 T0's own
-- finding) — subject-scope hold checking best-effort-extracts it from the
-- JSONB payload (present for TransactionPosted, absent for
-- TransactionReversed/AdjustmentDecided, in which case it simply never
-- matches a subject-scoped hold, same as any other class with no natural
-- subject identifier per fn_ledger_retention_hold_covers's own contract).
CREATE OR REPLACE FUNCTION fn_retention_purge_outbox_events_published(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'ledger.outbox_events.published';
    v_version  CONSTANT INT  := 1;
BEGIN
    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM outbox_events oe
        WHERE oe.status = 'published'
          AND oe.published_at < now() - INTERVAL '30 days'
          AND NOT fn_ledger_retention_hold_covers('outbox_events',
                COALESCE(oe.payload->>'user_id', oe.payload->>'target_user_id'),
                oe.aggregate_id::text, oe.published_at);

        INSERT INTO ledger_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'delete', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT oe.id
        FROM outbox_events oe
        WHERE oe.status = 'published'
          AND oe.published_at < now() - INTERVAL '30 days'
          AND NOT fn_ledger_retention_hold_covers('outbox_events',
                COALESCE(oe.payload->>'user_id', oe.payload->>'target_user_id'),
                oe.aggregate_id::text, oe.published_at)
        ORDER BY oe.published_at
        LIMIT p_batch_size
        FOR UPDATE OF oe SKIP LOCKED
    ), deleted AS (
        DELETE FROM outbox_events oe USING eligible WHERE oe.id = eligible.id RETURNING oe.id
    )
    SELECT count(*) INTO v_affected FROM deleted;

    INSERT INTO ledger_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'delete', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

GRANT EXECUTE ON FUNCTION fn_retention_purge_fee_quotes_unconsumed(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_fee_quotes_consumed(UUID, INT, BOOLEAN) TO app_service;
GRANT EXECUTE ON FUNCTION fn_retention_purge_outbox_events_published(UUID, INT, BOOLEAN) TO app_service;
