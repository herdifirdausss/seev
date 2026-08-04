-- docs/roadmap/archive/19 Task T1 (S3 butir 1): recurring/deferred user transactions
-- (auto-debit, scheduled transfer) executed by a daily job — no new
-- execution state machine. "Has this run" is answered by the ledger's own
-- idempotency key (sched:<id>:<run_date>), never a flag this table owns;
-- last_run_date/last_error here are informational only.
CREATE TABLE scheduled_transactions (
    id             UUID        PRIMARY KEY,
    user_id        UUID        NOT NULL,
    -- Narrow subset of processors.Command (pola pending_adjustments.cmd_payload,
    -- 16-T1) — validated at create time, never re-validated structurally at
    -- run time (a stored payload is trusted; RunDue only re-checks business
    -- state like balance via the normal posting pipeline).
    cmd_payload    JSONB       NOT NULL,
    schedule_kind  TEXT        NOT NULL CHECK (schedule_kind IN ('once','daily','monthly')),
    run_at_date    DATE        NOT NULL,
    -- 'monthly' only. 29-31 rejected at the application layer (avoid
    -- end-of-month edge cases — a user wanting "last day of month" is a
    -- product decision this MVP doesn't support).
    day_of_month   SMALLINT    NULL CHECK (day_of_month BETWEEN 1 AND 28),
    status         TEXT        NOT NULL DEFAULT 'active'
                   CHECK (status IN ('active','paused','finished','failed')),
    last_run_date  DATE        NULL,
    last_error     TEXT        NULL,
    created_by     TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sched_tx_due ON scheduled_transactions (run_at_date) WHERE status = 'active';
CREATE INDEX idx_sched_tx_user ON scheduled_transactions (user_id);

CREATE TRIGGER trg_scheduled_tx_ua BEFORE UPDATE ON scheduled_transactions
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

GRANT SELECT, INSERT, UPDATE ON scheduled_transactions TO app_service;
GRANT SELECT ON scheduled_transactions TO app_readonly;

ALTER TABLE scheduled_transactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE scheduled_transactions FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON scheduled_transactions FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON scheduled_transactions FOR SELECT TO app_readonly USING (true);
