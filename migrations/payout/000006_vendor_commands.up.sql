-- docs/roadmap/archive/45 Task T0 (K1): durable outbox for vendor dispatch commands.
-- Today (docs/roadmap/archive/40) provider.Submit is called inline in orchestrate.go's
-- submit(), outside any DB transaction — a crash between the ledger hold and
-- the vendor call, or between the vendor call and recording its outcome,
-- relies entirely on the resume job's next-tick retry loop, not a durable
-- queue. This table gives every "dispatch this payout to this vendor"
-- attempt a durable row a relay worker (Task T1) claims with
-- FOR UPDATE SKIP LOCKED, exactly like the ledger's outbox_events
-- (internal/ledger/repository/outbox_event_repository.go).
--
-- A command is NOT the audit trail (that remains payout_vendor_calls,
-- docs/roadmap/archive/40 Task T3, untouched) — it is the WORK ITEM that causes a
-- payout_vendor_calls row (and a breaker Record*, and a terminal-state
-- transition) to eventually happen. Delivery is honestly at-least-once:
-- a network timeout can never prove the vendor didn't receive the call, so
-- retries reuse the SAME vendor-facing idempotency key
-- (payout_requests.id, unchanged) rather than claiming exactly-once.
CREATE TABLE payout_vendor_commands (
    id                 UUID        PRIMARY KEY,
    -- Internal dedup key, format "payout:<request_id>:submit:<attempt>" —
    -- distinct from the vendor-facing idempotency key, which stays
    -- payout_request_id itself (docs/roadmap/archive/40) so retries of the SAME
    -- command never create a second payout at the vendor.
    command_key        TEXT        NOT NULL UNIQUE,
    payout_request_id  UUID        NOT NULL REFERENCES payout_requests(id),
    vendor             TEXT        NOT NULL,
    attempt            INT         NOT NULL CHECK (attempt > 0),
    status             TEXT        NOT NULL CHECK (
                            status IN ('pending', 'processing', 'failed', 'completed', 'dead')
                        ),
    retry_count        INT         NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    max_retries        INT         NOT NULL DEFAULT 8 CHECK (max_retries > 0),
    next_attempt_at    TIMESTAMPTZ NULL,
    last_attempted_at  TIMESTAMPTZ NULL,
    locked_at          TIMESTAMPTZ NULL,
    last_error         TEXT        NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (payout_request_id, attempt)
);

-- Relay claim query filters on status + next_attempt_at, orders by
-- created_at — one index covers both ClaimPending (status='pending') and
-- ClaimFailedForRetry (status='failed' AND next_attempt_at <= now()).
CREATE INDEX idx_payout_vendor_commands_claim
    ON payout_vendor_commands (status, next_attempt_at, created_at);

-- At most ONE live command (pending/processing/failed) per payout request
-- at any time — this is what makes EnsureSubmitCommand's recovery insert
-- and CompleteAndEnqueueFailover's next-attempt insert safe under
-- multi-replica concurrency: a second concurrent attempt to create a live
-- command for the same request conflicts on this index instead of
-- silently producing two commands racing to call the vendor.
CREATE UNIQUE INDEX idx_payout_vendor_commands_one_live
    ON payout_vendor_commands (payout_request_id)
    WHERE status IN ('pending', 'processing', 'failed');

-- Same grant/RLS strictness as payout_requests (migrations/000001): the app
-- role needs UPDATE (claim/complete/fail transitions), never DELETE (dead
-- commands stay visible to the operator, replayed via admin action, not
-- removed).
GRANT SELECT, INSERT, UPDATE ON payout_vendor_commands TO app_service;
GRANT SELECT ON payout_vendor_commands TO app_readonly;

ALTER TABLE payout_vendor_commands ENABLE ROW LEVEL SECURITY;
ALTER TABLE payout_vendor_commands FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service    ON payout_vendor_commands FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly  ON payout_vendor_commands FOR SELECT TO app_readonly USING (true);
