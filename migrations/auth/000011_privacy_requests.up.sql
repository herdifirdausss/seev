-- docs/roadmap/active/51-a8-data-lifecycle-privacy.md T4 (K9): auth-owned coordination
-- table for the authenticated user export flow. `cutoff` is fixed at
-- creation time and passed unchanged to every owner's export query, so a
-- slow multi-owner assembly never captures a moving target — two owners
-- queried minutes apart still agree on "as of when."
--
-- object_key/manifest_hash/expires_at are all NULL until the worker marks
-- the request 'ready' — a request that never gets that far (failed,
-- still collecting) has nothing in the object store to clean up.
CREATE TABLE privacy_requests (
    id             UUID        PRIMARY KEY,
    user_id        UUID        NOT NULL REFERENCES auth_users(id),
    status         TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN
                     ('pending', 'collecting', 'ready', 'failed', 'expired')),
    schema_version INT         NOT NULL DEFAULT 1,
    cutoff         TIMESTAMPTZ NOT NULL,
    object_key     TEXT,
    manifest_hash  TEXT,
    row_count      INT,
    error_message  TEXT,
    requested_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    ready_at       TIMESTAMPTZ,
    -- expires_at is set once ready (ready_at + 24h, K9's own "an
    -- undownloaded export expires after 24 hours") — a request that never
    -- reaches 'ready' has no expiry of its own to enforce.
    expires_at     TIMESTAMPTZ,
    -- Set on the FIRST successful download — K9's "one-time streaming
    -- download": once populated, the download handler refuses further
    -- attempts (the object itself is enqueued for deletion at that same
    -- moment, so a second attempt would fail against the store anyway,
    -- but this column is what makes that refusal an explicit business
    -- rule rather than an incidental storage-layer 404).
    downloaded_at  TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- K9: "at most one active export is allowed per user" — a partial unique
-- index (not an application-level check) so a race between two concurrent
-- create-export requests fails the loser's INSERT rather than depending on
-- a check-then-insert window.
CREATE UNIQUE INDEX uq_privacy_requests_active_per_user
    ON privacy_requests (user_id)
    WHERE status IN ('pending', 'collecting');

CREATE INDEX idx_privacy_requests_pending ON privacy_requests (requested_at) WHERE status = 'pending';
-- Feeds the TTL-expiry sweep — only rows that are ready, not yet
-- downloaded, and past their own expiry are ever eligible.
CREATE INDEX idx_privacy_requests_expiring ON privacy_requests (expires_at) WHERE status = 'ready' AND downloaded_at IS NULL;

GRANT SELECT, INSERT, UPDATE ON privacy_requests TO app_service;
GRANT SELECT ON privacy_requests TO app_readonly;

ALTER TABLE privacy_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE privacy_requests FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON privacy_requests
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON privacy_requests
    FOR SELECT TO app_readonly USING (true);
