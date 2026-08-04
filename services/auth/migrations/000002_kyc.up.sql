-- Phase 7d T1: KYC tier state and review submissions.

ALTER TABLE auth_users
    ADD COLUMN kyc_level SMALLINT NOT NULL DEFAULT 0
    CHECK (kyc_level IN (0, 1, 2));

CREATE TABLE kyc_submissions (
    id              UUID PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES auth_users(id),
    level_requested SMALLINT NOT NULL CHECK (level_requested IN (1, 2)),
    status          TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'rejected')),
    payload         JSONB NOT NULL,
    provider        TEXT NOT NULL,
    provider_ref    TEXT,
    decided_by      TEXT,
    decision_reason TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at      TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_kyc_submissions_one_pending
    ON kyc_submissions (user_id)
    WHERE status = 'pending';

GRANT SELECT, INSERT, UPDATE ON kyc_submissions TO app_service;
GRANT SELECT ON auth_users, kyc_submissions TO app_readonly;

ALTER TABLE kyc_submissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE kyc_submissions FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON kyc_submissions
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON kyc_submissions
    FOR SELECT TO app_readonly USING (true);
