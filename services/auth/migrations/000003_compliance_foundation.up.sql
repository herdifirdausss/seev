-- Plan 46 T1: durable KYC apply intents and the audit/metadata tables used by
-- the later compliance tasks.  The retry row is deliberately kept in the
-- auth database: auth owns the approval state and never needs ledger DB
-- credentials.

CREATE TABLE kyc_apply_retries (
    id              UUID PRIMARY KEY,
    submission_id   UUID NOT NULL REFERENCES kyc_submissions(id),
    user_id         UUID NOT NULL REFERENCES auth_users(id),
    level           SMALLINT NOT NULL CHECK (level IN (1, 2)),
    status          TEXT NOT NULL CHECK (status IN ('pending', 'succeeded', 'dead')),
    retry_count     INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT,
    locked_until    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_kyc_apply_retries_one_pending
    ON kyc_apply_retries (submission_id)
    WHERE status = 'pending';
CREATE INDEX idx_kyc_apply_retries_due
    ON kyc_apply_retries (next_attempt_at, id)
    WHERE status = 'pending';

CREATE TABLE kyc_level_changes (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES auth_users(id),
    from_level  SMALLINT NOT NULL CHECK (from_level IN (0, 1, 2)),
    to_level    SMALLINT NOT NULL CHECK (to_level IN (0, 1, 2)),
    direction   TEXT NOT NULL CHECK (direction IN ('upgrade', 'downgrade')),
    reason      TEXT,
    decided_by  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_kyc_level_changes_user_created
    ON kyc_level_changes (user_id, created_at DESC, id DESC);

-- Metadata only for now.  T6 stores encrypted bytes in object storage and
-- keeps only this non-sensitive envelope metadata in Postgres.
CREATE TABLE kyc_documents (
    id            UUID PRIMARY KEY,
    submission_id UUID NOT NULL REFERENCES kyc_submissions(id),
    user_id       UUID NOT NULL REFERENCES auth_users(id),
    object_key    TEXT NOT NULL,
    sha256        TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes    BIGINT NOT NULL CHECK (size_bytes > 0),
    content_type  TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_kyc_documents_submission ON kyc_documents (submission_id, created_at, id);

GRANT SELECT, INSERT, UPDATE ON kyc_apply_retries, kyc_level_changes, kyc_documents TO app_service;
GRANT SELECT ON kyc_apply_retries, kyc_level_changes, kyc_documents TO app_readonly;

ALTER TABLE kyc_apply_retries ENABLE ROW LEVEL SECURITY;
ALTER TABLE kyc_apply_retries FORCE ROW LEVEL SECURITY;
ALTER TABLE kyc_level_changes ENABLE ROW LEVEL SECURITY;
ALTER TABLE kyc_level_changes FORCE ROW LEVEL SECURITY;
ALTER TABLE kyc_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE kyc_documents FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON kyc_apply_retries
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON kyc_apply_retries
    FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_all_service ON kyc_level_changes
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON kyc_level_changes
    FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_all_service ON kyc_documents
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON kyc_documents
    FOR SELECT TO app_readonly USING (true);
