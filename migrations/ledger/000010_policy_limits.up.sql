-- docs/plan/17 Task T1 (S1): per-user/per-type limits + velocity for
-- internal/policy — evaluated in the ledger transport layer BEFORE
-- ledger.Post, never inside the ledger module itself.
--
-- NULL user_id = default limit for that transaction_type (applies to every
-- user without a specific override). A row with a specific user_id
-- overrides the default for that user+type pair. All limit columns are
-- NULLable — NULL means that dimension is unbounded.
CREATE TABLE policy_limits (
    id                  UUID        PRIMARY KEY,
    user_id             UUID        NULL,
    transaction_type    TEXT        NOT NULL,
    max_per_tx          BIGINT      NULL CHECK (max_per_tx > 0),
    max_daily_amount    BIGINT      NULL CHECK (max_daily_amount > 0),
    max_daily_count     INT         NULL CHECK (max_daily_count > 0),
    max_monthly_amount  BIGINT      NULL CHECK (max_monthly_amount > 0),
    enabled             BOOLEAN     NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (user_id, transaction_type)
);

-- NULL user_id must still be unique per transaction_type — same partial
-- unique index pattern as uq_ltx_idempotency (migrations/000001).
CREATE UNIQUE INDEX uq_policy_limits_default ON policy_limits(transaction_type)
    WHERE user_id IS NULL;

CREATE INDEX idx_policy_limits_user ON policy_limits(user_id) WHERE user_id IS NOT NULL;

CREATE TRIGGER trg_policy_limits_ua BEFORE UPDATE ON policy_limits
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Every new table since migrations/000009 carries its own grant+RLS —
-- don't wait for a second collective RLS migration (docs/plan/16 Task T3
-- established this pattern).
GRANT SELECT, INSERT, UPDATE ON policy_limits TO app_service;
GRANT SELECT ON policy_limits TO app_readonly;

ALTER TABLE policy_limits ENABLE ROW LEVEL SECURITY;
ALTER TABLE policy_limits FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON policy_limits FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON policy_limits FOR SELECT TO app_readonly USING (true);
