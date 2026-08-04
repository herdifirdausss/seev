-- Auth remains the source of identity truth. This projection is the ledger's
-- execution-time safety gate for queued commands and is synchronized by the
-- auth/ledger integration. Missing rows fail closed for gated sources.
CREATE TABLE money_movement_execution_subjects (
    user_id             UUID NOT NULL,
    tenant_id           UUID,
    status              TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'disabled', 'closing', 'closed')),
    kyc_level           INTEGER NOT NULL DEFAULT 0 CHECK (kyc_level >= 0),
    kyc_verified_until  TIMESTAMPTZ,
    tenant_status       TEXT NOT NULL DEFAULT 'active'
        CHECK (tenant_status IN ('active', 'disabled', 'closing', 'closed')),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (user_id, tenant_id)
);

CREATE INDEX idx_money_movement_execution_subjects_user
    ON money_movement_execution_subjects (user_id, updated_at DESC);

GRANT SELECT, INSERT, UPDATE ON money_movement_execution_subjects TO app_service;
GRANT SELECT ON money_movement_execution_subjects TO app_readonly;
ALTER TABLE money_movement_execution_subjects ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_movement_execution_subjects FORCE ROW LEVEL SECURITY;
CREATE POLICY pol_execution_subject_service ON money_movement_execution_subjects
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_execution_subject_read_only ON money_movement_execution_subjects
    FOR SELECT TO app_readonly USING (true);
