-- Phase 7d T5: immutable-ish templates for the effective per-user limits
-- provisioned when auth approves a KYC tier.
CREATE TABLE policy_tier_limits (
    kyc_level          SMALLINT NOT NULL CHECK (kyc_level IN (1, 2)),
    transaction_type   TEXT NOT NULL,
    max_per_tx         BIGINT,
    max_daily_amount   BIGINT,
    max_daily_count    INT,
    max_monthly_amount BIGINT,
    PRIMARY KEY (kyc_level, transaction_type)
);

INSERT INTO policy_tier_limits
    (kyc_level, transaction_type, max_per_tx, max_daily_amount, max_daily_count, max_monthly_amount)
VALUES
    (1, 'transfer_p2p',      1000000,   5000000,   20,  50000000),
    (1, 'money_in',           5000000,  10000000,    5, 100000000),
    (1, 'withdraw_initiate',  1000000,   5000000,    5,  50000000),
    (2, 'transfer_p2p',    100000000, 500000000,  100, 5000000000),
    (2, 'money_in',         500000000,1000000000,   50,10000000000),
    (2, 'withdraw_initiate',100000000,500000000,   50, 5000000000);

GRANT SELECT ON policy_tier_limits TO app_service, app_readonly;

ALTER TABLE policy_tier_limits ENABLE ROW LEVEL SECURITY;
ALTER TABLE policy_tier_limits FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_read_service ON policy_tier_limits
    FOR SELECT TO app_service USING (true);
CREATE POLICY pol_read_readonly ON policy_tier_limits
    FOR SELECT TO app_readonly USING (true);
