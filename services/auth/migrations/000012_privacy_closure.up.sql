-- docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T5 (K10, K11): extends
-- privacy_requests (built for T4's export flow) with the state an account
-- closure saga needs, and lets auth_users represent a user mid-closure.
--
-- Deliberately the SAME table as export, not a new one: K10's own blocking
-- condition list includes "a pending... privacy export" when closing, and
-- reusing privacy_requests means the existing uq_privacy_requests_active_per_user
-- partial unique index enforces "at most one active privacy request of
-- EITHER kind per user" for free, at the database level, without a second
-- cross-table check.
ALTER TABLE privacy_requests
    ADD COLUMN request_type TEXT NOT NULL DEFAULT 'export' CHECK (request_type IN ('export', 'closure')),
    -- Set only for request_type='closure'. surrogate_id is generated once,
    -- at request creation, and never changes across retries — regenerating
    -- it on retry would let a crash between "surrogate committed at one
    -- owner" and "saga restarts" orphan that owner's rows under a
    -- surrogate no other owner will ever commit to.
    ADD COLUMN surrogate_id UUID,
    -- The original subject UUID, encrypted, held ONLY while the saga is
    -- active (K10: "destroy the active-saga ciphertext" on finalization).
    -- Not a cryptox.Ring column in the T2 sense (no per-field AAD scheme
    -- here) — sealed directly by services/auth's own closure code using a
    -- dedicated ClosureConfig key namespace, mirroring T4's ExportConfig.
    -- No separate key-version column: cryptox.Ring's envelope self-describes
    -- the version it was sealed under (EnvelopeKeyVersion), the same
    -- convention T4's export archives already rely on.
    ADD COLUMN active_subject_ciphertext BYTEA,
    -- Per-owner saga progress, e.g. {"ledger": {"phase": "committed", ...}}
    -- — lets a resumed saga skip owners already durably committed instead
    -- of re-deriving from scratch (each owner's own commit is idempotent
    -- regardless, but the checkpoint avoids redundant internal calls and
    -- is what "resumes forward from the last durable owner state" in K11
    -- refers to).
    ADD COLUMN owner_checkpoints JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN retry_count INT NOT NULL DEFAULT 0,
    ADD COLUMN next_attempt_at TIMESTAMPTZ,
    ADD COLUMN last_error TEXT;

ALTER TABLE privacy_requests DROP CONSTRAINT privacy_requests_status_check;
ALTER TABLE privacy_requests ADD CONSTRAINT privacy_requests_status_check CHECK (status IN (
    -- export (T4) statuses, unchanged:
    'pending', 'collecting', 'ready', 'failed', 'expired',
    -- closure (T5) statuses: preparing = owners' Prepare in flight,
    -- committing = owners' Commit in flight (access already disabled),
    -- completed = auth finalized last, blocked = a K10 blocking condition
    -- fired (terminal, no auto-retry — the user must resolve it and submit
    -- a new closure request), dead = retry budget exhausted on a
    -- transient/infra failure (operator must investigate and replay).
    'preparing', 'committing', 'completed', 'blocked', 'dead'
));

-- Re-include the new in-progress closure statuses so "at most one active
-- request per user" covers closure too, not just export.
DROP INDEX uq_privacy_requests_active_per_user;
CREATE UNIQUE INDEX uq_privacy_requests_active_per_user
    ON privacy_requests (user_id)
    WHERE status IN ('pending', 'collecting', 'preparing', 'committing');

-- Feeds the closure saga worker's claim query, same SKIP LOCKED convention
-- as every other worker in this codebase.
CREATE INDEX idx_privacy_requests_closure_pending ON privacy_requests (COALESCE(next_attempt_at, requested_at))
    WHERE request_type = 'closure' AND status IN ('pending', 'preparing', 'committing');

-- K10: closure disables login/refresh immediately, before the saga even
-- starts committing owners. auth.go's existing Login/Refresh already
-- reject any status != 'active' generically, so 'closing'/'closed' get
-- that enforcement for free with no new code at those two call sites.
ALTER TABLE auth_users DROP CONSTRAINT auth_users_status_check;
ALTER TABLE auth_users ADD CONSTRAINT auth_users_status_check CHECK (status IN ('active', 'disabled', 'closing', 'closed'));
