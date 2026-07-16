-- docs/plan/23 Task T1: payout orchestration state machine — a user
-- withdraw request tracked through hold -> vendor submission -> terminal
-- state (settled/cancelled/failed). Every state transition is an atomic
-- conditional UPDATE (WHERE status = $expected) at the repository layer —
-- this table has no trigger-level concurrency guard of its own; the
-- guard that actually prevents double-settle/settle-after-cancel is the
-- LEDGER's own closed_by_tx_id (docs/plan/14 Task T2, decision K3) on
-- hold_tx_id's withdraw_initiate — payout_requests.status is a read model
-- of that, reconciled after the fact, never the source of truth for money
-- movement itself.
CREATE TABLE payout_requests (
    id             UUID        PRIMARY KEY,     -- uuidv7; also the idempotency key toward the vendor (K-T6)
    user_id        UUID        NOT NULL,
    amount         BIGINT      NOT NULL CHECK (amount > 0),
    currency       CHAR(3)     NOT NULL,
    vendor         TEXT        NOT NULL,
    destination    JSONB       NOT NULL,        -- vendor-shaped destination (bank code, account no, ...)
    status         TEXT        NOT NULL DEFAULT 'created' CHECK (status IN
                    ('created','held','submitted','vendor_pending','settled','failed','cancelled')),
    hold_tx_id     UUID        NULL,            -- ledger tx id of the withdraw_initiate
    settle_tx_id   UUID        NULL,            -- ledger tx id of the withdraw_settle OR withdraw_cancel that closed hold_tx_id
    vendor_ref     TEXT        NULL,            -- vendor's own reference, set after Submit
    error_message  TEXT        NULL,
    created_by     TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_payout_requests_status ON payout_requests(status, updated_at);
CREATE INDEX idx_payout_requests_user   ON payout_requests(user_id, created_at DESC);

-- One row per outbound vendor call attempt — audit trail. req_summary is a
-- SUMMARY, never a full payload (destination/credentials must never land
-- here in the clear).
CREATE TABLE payout_vendor_calls (
    id          UUID        PRIMARY KEY,
    request_id  UUID        NOT NULL REFERENCES payout_requests(id),
    attempt     INT         NOT NULL,
    req_summary TEXT        NOT NULL,
    resp_status TEXT        NULL,
    error       TEXT        NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_payout_vendor_calls_request ON payout_vendor_calls(request_id, created_at);

-- app_service: SELECT+INSERT+UPDATE on payout_requests (status transitions),
-- SELECT+INSERT only on payout_vendor_calls (append-only audit trail, same
-- immutable-audit philosophy as screening_events, migrations/000017).
GRANT SELECT, INSERT, UPDATE ON payout_requests TO app_service;
GRANT SELECT, INSERT ON payout_vendor_calls TO app_service;
GRANT SELECT ON payout_requests, payout_vendor_calls TO app_readonly;

ALTER TABLE payout_requests    ENABLE ROW LEVEL SECURITY;
ALTER TABLE payout_requests    FORCE ROW LEVEL SECURITY;
ALTER TABLE payout_vendor_calls ENABLE ROW LEVEL SECURITY;
ALTER TABLE payout_vendor_calls FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service   ON payout_requests    FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON payout_requests    FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_select_service ON payout_vendor_calls FOR SELECT TO app_service USING (true);
CREATE POLICY pol_insert_service ON payout_vendor_calls FOR INSERT TO app_service WITH CHECK (true);
CREATE POLICY pol_read_readonly  ON payout_vendor_calls FOR SELECT TO app_readonly USING (true);
