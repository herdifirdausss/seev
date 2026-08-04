-- Security audit finding: chargeback_disputes.ResolveDispute set status/
-- resolved_at/resolution_reason but recorded no ACTOR at all — no way to
-- reconstruct who moved a case to 'lost' (accepting a financial loss) or
-- 'won' (denying a customer's dispute), and no history of intermediate
-- transitions either (open -> evidence_submitted). Mirrors
-- kyc_level_changes' own audit-trail shape (services/auth/migrations/000003) —
-- every transition, who made it, and why.
ALTER TABLE chargeback_disputes ADD COLUMN resolved_by TEXT NULL;

-- The original migration's "resolved fields together" CHECK was an
-- unnamed table-level constraint, so its actual name is whatever Postgres
-- auto-assigned — find it by elimination (the one CHECK on this table that
-- isn't one of the three known column-level checks) rather than guessing
-- the generated name.
DO $$
DECLARE
    v_name TEXT;
BEGIN
    SELECT c.conname INTO v_name
    FROM pg_constraint c
    WHERE c.conrelid = 'chargeback_disputes'::regclass
      AND c.contype = 'c'
      AND c.conname NOT IN ('chargeback_disputes_card_network_check', 'chargeback_disputes_amount_check', 'chargeback_disputes_status_check');
    IF v_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE chargeback_disputes DROP CONSTRAINT %I', v_name);
    END IF;
END;
$$;

ALTER TABLE chargeback_disputes
    ADD CONSTRAINT chk_chargeback_disputes_resolved_fields_together CHECK (
        (status IN ('won', 'lost', 'expired')) = (resolved_at IS NOT NULL AND resolved_by IS NOT NULL)
    );

CREATE TABLE chargeback_dispute_status_changes (
    id          UUID PRIMARY KEY,
    dispute_id  UUID NOT NULL REFERENCES chargeback_disputes(id),
    from_status TEXT NOT NULL,
    to_status   TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    changed_by  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_chargeback_dispute_status_changes_dispute
    ON chargeback_dispute_status_changes (dispute_id, created_at DESC);

GRANT SELECT, INSERT ON chargeback_dispute_status_changes TO app_service;
GRANT SELECT ON chargeback_dispute_status_changes TO app_readonly;

ALTER TABLE chargeback_dispute_status_changes ENABLE ROW LEVEL SECURITY;
ALTER TABLE chargeback_dispute_status_changes FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON chargeback_dispute_status_changes FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON chargeback_dispute_status_changes FOR SELECT TO app_readonly USING (true);
