CREATE TABLE payout_intake_control (
    id          SMALLINT PRIMARY KEY CHECK (id = 1),
    paused      BOOLEAN NOT NULL DEFAULT false,
    revision    BIGINT NOT NULL DEFAULT 0,
    updated_by  TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO payout_intake_control (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

CREATE TABLE payout_intake_commands (
    command_id          UUID PRIMARY KEY,
    action              TEXT NOT NULL CHECK (action IN ('pause','resume')),
    expected_revision   BIGINT NOT NULL,
    actor               TEXT NOT NULL,
    reason              TEXT NOT NULL,
    applied             BOOLEAN NOT NULL,
    resulting_revision  BIGINT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE ON payout_intake_control, payout_intake_commands TO app_service;
GRANT SELECT ON payout_intake_control, payout_intake_commands TO app_readonly;
ALTER TABLE payout_intake_control ENABLE ROW LEVEL SECURITY;
ALTER TABLE payout_intake_commands ENABLE ROW LEVEL SECURITY;
ALTER TABLE payout_intake_control FORCE ROW LEVEL SECURITY;
ALTER TABLE payout_intake_commands FORCE ROW LEVEL SECURITY;
CREATE POLICY payout_intake_control_service ON payout_intake_control FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY payout_intake_control_readonly ON payout_intake_control FOR SELECT TO app_readonly USING (true);
CREATE POLICY payout_intake_commands_service ON payout_intake_commands FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY payout_intake_commands_readonly ON payout_intake_commands FOR SELECT TO app_readonly USING (true);
