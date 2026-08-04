CREATE TABLE screening_events (
    id         UUID        PRIMARY KEY,
    tx_type    TEXT        NOT NULL,
    user_id    UUID        NOT NULL,
    amount     BIGINT      NOT NULL,
    currency   CHAR(3)     NOT NULL,
    rule       TEXT        NOT NULL,
    verdict    TEXT        NOT NULL CHECK (verdict IN ('flagged','blocked')),
    reason     TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_screening_events_user    ON screening_events(user_id, created_at DESC);
CREATE INDEX idx_screening_events_verdict ON screening_events(verdict, created_at DESC);

GRANT SELECT, INSERT ON screening_events TO app_service;
GRANT SELECT ON screening_events TO app_readonly;

ALTER TABLE screening_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE screening_events FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_select_service ON screening_events FOR SELECT TO app_service USING (true);
CREATE POLICY pol_insert_service ON screening_events FOR INSERT TO app_service WITH CHECK (true);
CREATE POLICY pol_read_readonly  ON screening_events FOR SELECT TO app_readonly USING (true);
