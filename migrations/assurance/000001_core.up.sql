CREATE TABLE assurance_runs (
    id                 UUID PRIMARY KEY,
    mode               TEXT NOT NULL CHECK (mode IN ('incremental','backfill','manual')),
    status             TEXT NOT NULL CHECK (status IN ('running','succeeded','failed')),
    baseline           BOOLEAN NOT NULL DEFAULT false,
    started_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at        TIMESTAMPTZ,
    records_scanned    INTEGER NOT NULL DEFAULT 0,
    findings_opened    INTEGER NOT NULL DEFAULT 0,
    error_code         TEXT NOT NULL DEFAULT '',
    error_message      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX assurance_runs_started_idx ON assurance_runs (started_at DESC, id DESC);

CREATE TABLE assurance_cursors (
    source             TEXT PRIMARY KEY CHECK (source IN ('payin','payout','ledger')),
    updated_at         TIMESTAMPTZ,
    resource_id        UUID,
    backfill_complete  BOOLEAN NOT NULL DEFAULT false,
    updated_by_run_id  UUID REFERENCES assurance_runs(id),
    updated_at_service TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE assurance_findings (
    id                 UUID PRIMARY KEY,
    fingerprint        TEXT NOT NULL UNIQUE,
    severity           TEXT NOT NULL CHECK (severity IN ('medium','high','critical')),
    rule_code          TEXT NOT NULL,
    resource_id        TEXT NOT NULL,
    amount_minor       BIGINT NOT NULL DEFAULT 0,
    currency           CHAR(3) NOT NULL DEFAULT '',
    evidence           JSONB NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    occurrence_count   BIGINT NOT NULL DEFAULT 1,
    status             TEXT NOT NULL CHECK (status IN ('open','acknowledged','resolved')),
    resolved_at        TIMESTAMPTZ,
    acknowledged_at    TIMESTAMPTZ,
    resolved_by        TEXT NOT NULL DEFAULT '',
    acknowledged_by    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX assurance_findings_status_idx ON assurance_findings (status, severity, last_seen_at DESC);
CREATE INDEX assurance_findings_rule_idx ON assurance_findings (rule_code, last_seen_at DESC);

CREATE TABLE assurance_alert_deliveries (
    id                 UUID PRIMARY KEY,
    finding_id         UUID NOT NULL REFERENCES assurance_findings(id),
    severity           TEXT NOT NULL,
    message            TEXT NOT NULL,
    status             TEXT NOT NULL CHECK (status IN ('pending','delivered','failed')),
    attempts           INTEGER NOT NULL DEFAULT 0,
    next_attempt_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error         TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at       TIMESTAMPTZ
);
CREATE INDEX assurance_alert_deliveries_pending_idx ON assurance_alert_deliveries (status, next_attempt_at);

CREATE TABLE intake_control_commands (
    id                 UUID PRIMARY KEY,
    flow               TEXT NOT NULL CHECK (flow IN ('payin','payout')),
    action             TEXT NOT NULL CHECK (action IN ('pause','resume_request','resume_approve')),
    revision           BIGINT NOT NULL,
    requested_by       TEXT NOT NULL,
    approved_by        TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL CHECK (status IN ('pending','applying','applied','rejected','failed')),
    idempotency_key    UUID NOT NULL UNIQUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    applied_at         TIMESTAMPTZ,
    error_code         TEXT NOT NULL DEFAULT '',
    error_message      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX intake_control_commands_flow_idx ON intake_control_commands (flow, created_at DESC);

GRANT SELECT, INSERT, UPDATE ON assurance_runs, assurance_cursors, assurance_findings, assurance_alert_deliveries, intake_control_commands TO app_service;
GRANT SELECT ON assurance_runs, assurance_cursors, assurance_findings, assurance_alert_deliveries, intake_control_commands TO app_readonly;

ALTER TABLE assurance_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE assurance_cursors ENABLE ROW LEVEL SECURITY;
ALTER TABLE assurance_findings ENABLE ROW LEVEL SECURITY;
ALTER TABLE assurance_alert_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE intake_control_commands ENABLE ROW LEVEL SECURITY;
ALTER TABLE assurance_runs FORCE ROW LEVEL SECURITY;
ALTER TABLE assurance_cursors FORCE ROW LEVEL SECURITY;
ALTER TABLE assurance_findings FORCE ROW LEVEL SECURITY;
ALTER TABLE assurance_alert_deliveries FORCE ROW LEVEL SECURITY;
ALTER TABLE intake_control_commands FORCE ROW LEVEL SECURITY;

CREATE POLICY assurance_runs_service ON assurance_runs FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY assurance_runs_readonly ON assurance_runs FOR SELECT TO app_readonly USING (true);
CREATE POLICY assurance_cursors_service ON assurance_cursors FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY assurance_cursors_readonly ON assurance_cursors FOR SELECT TO app_readonly USING (true);
CREATE POLICY assurance_findings_service ON assurance_findings FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY assurance_findings_readonly ON assurance_findings FOR SELECT TO app_readonly USING (true);
CREATE POLICY assurance_alert_deliveries_service ON assurance_alert_deliveries FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY assurance_alert_deliveries_readonly ON assurance_alert_deliveries FOR SELECT TO app_readonly USING (true);
CREATE POLICY intake_commands_service ON intake_control_commands FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY intake_commands_readonly ON intake_control_commands FOR SELECT TO app_readonly USING (true);
