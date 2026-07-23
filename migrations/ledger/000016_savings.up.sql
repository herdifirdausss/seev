-- docs/roadmap/archive/19 Task T3 (S8): daily interest accrual for savings-product
-- accounts. MVP has no product table — ops registers which accounts earn
-- interest explicitly via savings_config, never a magic pocket_code prefix.

ALTER TABLE accounts DROP CONSTRAINT accounts_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check CHECK (type IN
    ('cash','hold','pending','frozen','pocket','fee',
     'settlement','escrow','chargeback','confiscated','adjustment','suspense',
     'fx_conversion','interest_expense'));

CREATE TABLE savings_config (
    account_id      UUID        PRIMARY KEY REFERENCES accounts(id),
    annual_rate_bps INT         NOT NULL CHECK (annual_rate_bps BETWEEN 0 AND 2000),
    enabled         BOOLEAN     NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_savings_config_enabled ON savings_config(enabled) WHERE enabled = true;

CREATE TRIGGER trg_savings_config_ua BEFORE UPDATE ON savings_config
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Interest expense account per currency (docs/roadmap/archive/19 Task T3 step 2) —
-- allow_negative=true, same rationale as every other system expense/
-- position account: the platform's interest liability before it's funded.
INSERT INTO accounts (id, owner_type, type, currency, system_qualifier, created_by) VALUES
('00000000-0000-0000-0000-000000000029','system','interest_expense','IDR',NULL,'migration'),
('00000000-0000-0000-0000-000000000030','system','interest_expense','USD',NULL,'migration');

INSERT INTO account_balances (account_id, allow_negative) VALUES
('00000000-0000-0000-0000-000000000029', true),
('00000000-0000-0000-0000-000000000030', true);

GRANT SELECT, INSERT, UPDATE ON savings_config TO app_service;
GRANT SELECT ON savings_config TO app_readonly;

ALTER TABLE savings_config ENABLE ROW LEVEL SECURITY;
ALTER TABLE savings_config FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON savings_config FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON savings_config FOR SELECT TO app_readonly USING (true);
