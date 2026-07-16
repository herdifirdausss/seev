-- docs/plan/22 Task T2: payin webhook events — one row per vendor webhook
-- delivery, deduped by (vendor, vendor_event_id) and mapped to a ledger
-- money_in posting. Settled-webhook-only (docs/plan/22 scope; no payment
-- intents table yet).
CREATE TABLE payin_webhook_events (
    id              UUID        PRIMARY KEY,
    vendor          TEXT        NOT NULL,
    vendor_event_id TEXT        NOT NULL,
    external_ref    TEXT        NOT NULL,
    user_id         UUID        NOT NULL,
    amount          BIGINT      NOT NULL,
    currency        CHAR(3)     NOT NULL,
    raw             JSONB       NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'received'
                    CHECK (status IN ('received','posted','failed')),
    error_message   TEXT        NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (vendor, vendor_event_id)
);

CREATE INDEX idx_payin_webhook_events_status ON payin_webhook_events(status, created_at DESC);
CREATE INDEX idx_payin_webhook_events_vendor ON payin_webhook_events(vendor, created_at DESC);

-- app_service: SELECT+INSERT+UPDATE — UPDATE needed for the
-- received -> posted|failed status transition (docs/plan/22 Task T2 step
-- 1). Never DELETE — every delivery, including ignored/failed ones, stays
-- as a forensic/replay record, same immutable-except-status-column
-- philosophy as ledger_transactions itself.
GRANT SELECT, INSERT, UPDATE ON payin_webhook_events TO app_service;
GRANT SELECT ON payin_webhook_events TO app_readonly;

ALTER TABLE payin_webhook_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE payin_webhook_events FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service    ON payin_webhook_events FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly  ON payin_webhook_events FOR SELECT TO app_readonly USING (true);
