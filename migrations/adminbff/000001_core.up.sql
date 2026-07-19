CREATE TABLE sessions (
    id                  UUID PRIMARY KEY,
    user_id             UUID NOT NULL,
    email               TEXT NOT NULL,
    role                TEXT NOT NULL CHECK (role IN ('admin', 'admin_maker', 'admin_checker')),
    csrf_token          TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_sessions_expires ON sessions (expires_at);
CREATE INDEX idx_sessions_user ON sessions (user_id);

CREATE TABLE audit_log (
    id            BIGSERIAL PRIMARY KEY,
    user_id       UUID NOT NULL,
    email         TEXT NOT NULL,
    role          TEXT NOT NULL,
    method        TEXT NOT NULL,
    route_pattern TEXT NOT NULL,
    target_service TEXT NOT NULL,
    resource_id   TEXT NOT NULL DEFAULT '',
    outcome       INTEGER NOT NULL CHECK (outcome BETWEEN 100 AND 599),
    request_id    TEXT NOT NULL DEFAULT '',
    summary       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_log_created ON audit_log (created_at DESC, id DESC);
CREATE INDEX idx_audit_log_operator ON audit_log (user_id, created_at DESC);
CREATE INDEX idx_audit_log_service ON audit_log (target_service, created_at DESC);

GRANT SELECT, INSERT, UPDATE ON sessions TO app_service;
GRANT SELECT ON sessions TO app_readonly;
GRANT SELECT, INSERT ON audit_log TO app_service;
GRANT SELECT ON audit_log TO app_readonly;
GRANT USAGE, SELECT ON SEQUENCE audit_log_id_seq TO app_service;

ALTER TABLE sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE sessions FORCE ROW LEVEL SECURITY;
ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON sessions FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON sessions FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_insert_service ON audit_log FOR INSERT TO app_service WITH CHECK (true);
CREATE POLICY pol_read_service ON audit_log FOR SELECT TO app_service USING (true);
CREATE POLICY pol_read_readonly ON audit_log FOR SELECT TO app_readonly USING (true);
