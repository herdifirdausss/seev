-- docs/roadmap/archive/18 Task T1 (S2): currency registry — single source of truth for
-- which currencies the platform supports and their minor-unit exponent.
-- No FK from accounts.currency/ledger_transactions.currency to this table
-- (those stay plain CHAR(3) with no FK, consistent with owner_id having no
-- FK in migrations/000001) — adding one now means a full table rewrite +
-- lock on insert-heavy tables for a validation application code already
-- performs. Validity is enforced in Go via currency.IsValid.
CREATE TABLE currencies (
    code       CHAR(3)     PRIMARY KEY,
    minor_unit SMALLINT    NOT NULL CHECK (minor_unit BETWEEN 0 AND 4),
    enabled    BOOLEAN     NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO currencies (code, minor_unit) VALUES ('IDR', 0), ('USD', 2);

GRANT SELECT, INSERT, UPDATE ON currencies TO app_service;
GRANT SELECT ON currencies TO app_readonly;

ALTER TABLE currencies ENABLE ROW LEVEL SECURITY;
ALTER TABLE currencies FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON currencies FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON currencies FOR SELECT TO app_readonly USING (true);
