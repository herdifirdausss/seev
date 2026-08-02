-- Security audit finding: vendor_callback_inbox and vendor_outbound_attempts
-- were the only two tables in the entire schema (all ~90+ others) with no
-- Row-Level Security at all — every sibling table, including this same
-- migration directory's own vendor_retention_holds/vendor_retention_audit
-- (migrations/vendor/000002), gets ENABLE+FORCE ROW LEVEL SECURITY as
-- defense-in-depth against grant creep (see migrations/ledger/000009_rls_roles.up.sql's
-- own comment: FORCE means even a future accidental broader GRANT, or a
-- schema-owner connection used for ad-hoc debugging, still needs an
-- explicit policy to see rows). This closes that gap, scoped to vendor_app
-- specifically — the same role (and the same conditional-existence check)
-- migrations/vendor/000001_vendor_boundary.up.sql's own GRANT already uses,
-- since these two tables deliberately never got a cross-service app_service
-- grant (that migration's own comment: "VendorService writes only its own
-- boundary rows").
--
-- Also closes a second finding: vendor_callback_inbox.raw_body (the FULL,
-- unredacted vendor webhook payload — potentially bank/card/customer data)
-- and .selected_headers had no cryptox protection at all, unlike every
-- other raw-payload column in the codebase (payin_webhook_events.raw,
-- kyc_submissions.payload). Both columns are write-only from Go's
-- perspective (grep confirms internal/vendorboundary/callback.go's Ensure()
-- is the only read OR write site in the whole codebase — nothing ever
-- selects them back), so unlike the auth/payin cryptox rollouts this
-- doesn't need a live expand/backfill/contract sequence for a read
-- dependency that must keep working mid-rollout: this migration cuts over
-- directly. Existing plaintext rows lose their raw_body/selected_headers on
-- this ALTER (accepted — matches the "down migrations may lose data on
-- rollback" precedent already used elsewhere, e.g.
-- migrations/ledger/000035_chargeback_disputes.down.sql); rows already past
-- their 30-day redaction window would have been overwritten with a fixed
-- '{"redacted":true}' marker anyway (migrations/vendor/000003), so this only
-- affects raw bytes from the last 30 days in a non-terminal-or-recent state.
ALTER TABLE vendor_callback_inbox
    ADD COLUMN raw_body_ciphertext BYTEA,
    ADD COLUMN raw_body_key_version INT,
    ADD COLUMN selected_headers_ciphertext BYTEA,
    ADD COLUMN selected_headers_key_version INT,
    DROP COLUMN raw_body,
    DROP COLUMN selected_headers;

-- fn_retention_purge_callback_inbox_raw (000003) redacted by overwriting the
-- old plaintext columns with a fixed marker; redaction of an already-opaque
-- ciphertext column needs no marker — NULL is equally opaque and simpler.
-- policy_version bumped 1->2 to reflect the redaction mechanism change (the
-- audit table's own policy_version column exists exactly to track this).
CREATE OR REPLACE FUNCTION fn_retention_purge_callback_inbox_raw(
    p_job_id UUID, p_batch_size INT, p_dry_run BOOLEAN
) RETURNS INT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
    v_affected INT;
    v_started  TIMESTAMPTZ := clock_timestamp();
    v_class    CONSTANT TEXT := 'vendor.callback_inbox';
    v_version  CONSTANT INT  := 2;
BEGIN
    IF p_batch_size < 1 OR p_batch_size > 500 THEN
        RAISE EXCEPTION 'invalid retention batch size';
    END IF;

    IF p_dry_run THEN
        SELECT count(*) INTO v_affected
        FROM vendor_callback_inbox c
        WHERE c.processing_status IN ('finalized', 'ignored', 'unmatched', 'dead')
          AND c.updated_at < now() - INTERVAL '30 days'
          AND c.raw_body_ciphertext IS NOT NULL;

        INSERT INTO vendor_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
        VALUES (p_job_id, v_class, 'redact', true, v_affected, v_version, v_started, 'ok');
        RETURN v_affected;
    END IF;

    WITH eligible AS (
        SELECT c.id
        FROM vendor_callback_inbox c
        WHERE c.processing_status IN ('finalized', 'ignored', 'unmatched', 'dead')
          AND c.updated_at < now() - INTERVAL '30 days'
          AND c.raw_body_ciphertext IS NOT NULL
        ORDER BY c.updated_at
        LIMIT p_batch_size
        FOR UPDATE OF c SKIP LOCKED
    ), redacted AS (
        UPDATE vendor_callback_inbox c
        SET raw_body_ciphertext = NULL, raw_body_key_version = NULL,
            selected_headers_ciphertext = NULL, selected_headers_key_version = NULL
        FROM eligible WHERE c.id = eligible.id RETURNING c.id
    )
    SELECT count(*) INTO v_affected FROM redacted;

    INSERT INTO vendor_retention_audit (job_id, class, action, dry_run, affected_count, policy_version, started_at, result)
    VALUES (p_job_id, v_class, 'redact', false, v_affected, v_version, v_started, 'ok');
    RETURN v_affected;
END;
$$;

ALTER TABLE vendor_callback_inbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE vendor_callback_inbox FORCE ROW LEVEL SECURITY;
ALTER TABLE vendor_outbound_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE vendor_outbound_attempts FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'vendor_app') THEN
        CREATE POLICY pol_vendor_app ON vendor_callback_inbox FOR ALL TO vendor_app USING (true) WITH CHECK (true);
        CREATE POLICY pol_vendor_app ON vendor_outbound_attempts FOR ALL TO vendor_app USING (true) WITH CHECK (true);
    END IF;
END;
$$;
