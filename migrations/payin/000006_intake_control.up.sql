CREATE TABLE payin_intake_control (
    id          SMALLINT PRIMARY KEY CHECK (id = 1),
    paused      BOOLEAN NOT NULL DEFAULT false,
    revision    BIGINT NOT NULL DEFAULT 0,
    updated_by  TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO payin_intake_control (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

CREATE TABLE payin_intake_commands (
    command_id          UUID PRIMARY KEY,
    action              TEXT NOT NULL CHECK (action IN ('pause','resume')),
    expected_revision   BIGINT NOT NULL,
    actor               TEXT NOT NULL,
    reason              TEXT NOT NULL,
    applied             BOOLEAN NOT NULL,
    resulting_revision  BIGINT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE ON payin_intake_control, payin_intake_commands TO app_service;
GRANT SELECT ON payin_intake_control, payin_intake_commands TO app_readonly;
ALTER TABLE payin_intake_control ENABLE ROW LEVEL SECURITY;
ALTER TABLE payin_intake_commands ENABLE ROW LEVEL SECURITY;
ALTER TABLE payin_intake_control FORCE ROW LEVEL SECURITY;
ALTER TABLE payin_intake_commands FORCE ROW LEVEL SECURITY;
CREATE POLICY payin_intake_control_service ON payin_intake_control FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY payin_intake_control_readonly ON payin_intake_control FOR SELECT TO app_readonly USING (true);
CREATE POLICY payin_intake_commands_service ON payin_intake_commands FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY payin_intake_commands_readonly ON payin_intake_commands FOR SELECT TO app_readonly USING (true);
