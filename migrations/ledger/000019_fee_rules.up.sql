-- docs/plan/33 Task T1: database-driven fee rules. NULL user_id is the
-- default for every user; an empty gateway is the default for every route.
CREATE TABLE fee_rules (
    id                  UUID        PRIMARY KEY,
    tx_type             TEXT        NOT NULL,
    gateway             TEXT        NOT NULL DEFAULT '',
    currency            TEXT        NOT NULL,
    user_id             UUID        NULL,
    flat_minor_units    BIGINT      NOT NULL DEFAULT 0,
    percent_basis_pts   BIGINT      NOT NULL DEFAULT 0
        CHECK (percent_basis_pts >= 0 AND percent_basis_pts < 10000),
    fee_gateway         TEXT        NOT NULL DEFAULT 'platform',
    enabled             BOOLEAN     NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE NULLS NOT DISTINCT (tx_type, gateway, currency, user_id)
);

CREATE INDEX idx_fee_rules_lookup ON fee_rules(tx_type, currency) WHERE enabled;

CREATE TRIGGER trg_fee_rules_ua BEFORE UPDATE ON fee_rules
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

GRANT SELECT, INSERT, UPDATE ON fee_rules TO app_service;
GRANT SELECT ON fee_rules TO app_readonly;

ALTER TABLE fee_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE fee_rules FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON fee_rules FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON fee_rules FOR SELECT TO app_readonly USING (true);
