-- Plan 46 T3: per-rule screening modes.  The env mode remains the fallback
-- for a rule without an override; changing this table does not require a
-- fraud-service restart.
CREATE TABLE screening_rule_modes (
    rule       TEXT PRIMARY KEY,
    mode       TEXT NOT NULL CHECK (mode IN ('off', 'monitor', 'block')),
    updated_by TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO screening_rule_modes (rule, mode, updated_by)
VALUES
    ('amount_threshold', 'off', 'migration'),
    ('velocity_anomaly', 'off', 'migration'),
    ('sanctions_watchlist', 'off', 'migration')
ON CONFLICT (rule) DO NOTHING;

GRANT SELECT, INSERT, UPDATE ON screening_rule_modes TO app_service;
GRANT SELECT ON screening_rule_modes TO app_readonly;

ALTER TABLE screening_rule_modes ENABLE ROW LEVEL SECURITY;
ALTER TABLE screening_rule_modes FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON screening_rule_modes
    FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON screening_rule_modes
    FOR SELECT TO app_readonly USING (true);
