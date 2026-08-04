-- docs/roadmap/active/57-c1-merchant-b2b-api.md T9's own "merchant
-- suspension and global route-disable controls" — a generic key/value
-- settings table rather than a single hardcoded boolean column, so a
-- future operational toggle needs no new migration. The ABSENCE of a row
-- for a given key is always treated as "default/enabled" at the
-- application layer (services/gateway/internal/merchant.GlobalFlag) — an operator must
-- take an explicit action to disable anything; a fresh deployment or a
-- row that was never touched behaves exactly as if this table did not
-- exist.
CREATE TABLE merchant_settings (
    key         TEXT        PRIMARY KEY,
    value       TEXT        NOT NULL,
    updated_by  TEXT        NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE ON merchant_settings TO app_service;
GRANT SELECT ON merchant_settings TO app_readonly;

ALTER TABLE merchant_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE merchant_settings FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_merchant_settings_service   ON merchant_settings FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY pol_merchant_settings_readonly  ON merchant_settings FOR SELECT TO app_readonly USING (true);
