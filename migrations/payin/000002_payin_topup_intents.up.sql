-- docs/plan/25 Task T3: payin topup intents — lets a user initiate a
-- top-up (POST /api/v1/topup) and get a `reference` to quote at the
-- vendor, without the vendor ever needing to know the internal user_id
-- (it travels back in the existing payin_webhook_events.external_ref
-- field, zero vendorgw/mockvendor changes).
CREATE TABLE payin_topup_intents (
    id               UUID        PRIMARY KEY,
    reference        TEXT        NOT NULL UNIQUE,
    user_id          UUID        NOT NULL,
    amount           BIGINT      NOT NULL CHECK (amount > 0),
    currency         CHAR(3)     NOT NULL,
    vendor           TEXT        NOT NULL,
    status           TEXT        NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','settled','expired')),
    settled_event_id UUID        NULL REFERENCES payin_webhook_events(id),
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_topup_intents_user ON payin_topup_intents(user_id, created_at DESC);

-- app_service: SELECT+INSERT+UPDATE — UPDATE needed for the
-- pending -> settled|expired status transition. Never DELETE — same
-- immutable-except-status-column philosophy as payin_webhook_events.
GRANT SELECT, INSERT, UPDATE ON payin_topup_intents TO app_service;
GRANT SELECT ON payin_topup_intents TO app_readonly;

ALTER TABLE payin_topup_intents ENABLE ROW LEVEL SECURITY;
ALTER TABLE payin_topup_intents FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service    ON payin_topup_intents FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly  ON payin_topup_intents FOR SELECT TO app_readonly USING (true);
