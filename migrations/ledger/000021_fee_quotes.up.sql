-- docs/plan/38 Task T1: fee quotes — a user requests a quote before
-- committing to a transaction; the quoted fee is honored EXACTLY at posting
-- time or the request is rejected (422 QUOTE_EXPIRED/QUOTE_MISMATCH),
-- never silently repriced even if fee_rules changes in between.
CREATE TABLE fee_quotes (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    transaction_type TEXT NOT NULL,
    gateway TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL,
    amount BIGINT NOT NULL CHECK (amount > 0),
    fee_amount BIGINT NOT NULL CHECK (fee_amount >= 0 AND fee_amount < amount),
    fee_gateway TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    consumed_by_ref TEXT,          -- 'tx:<uuid>' | 'payout:<uuid>'
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_fee_quotes_user ON fee_quotes(user_id, created_at);
CREATE INDEX idx_fee_quotes_expiry ON fee_quotes(expires_at) WHERE consumed_at IS NULL;

GRANT SELECT, INSERT, UPDATE ON fee_quotes TO app_service;
GRANT SELECT ON fee_quotes TO app_readonly;

ALTER TABLE fee_quotes ENABLE ROW LEVEL SECURITY;
ALTER TABLE fee_quotes FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service ON fee_quotes FOR ALL TO app_service USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON fee_quotes FOR SELECT TO app_readonly USING (true);
