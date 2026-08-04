DELETE FROM policy_tier_limits WHERE kyc_level = 0;
DELETE FROM policy_limits WHERE max_per_tx = 0 OR max_daily_amount = 0 OR max_daily_count = 0 OR max_monthly_amount = 0;
ALTER TABLE policy_limits
    DROP CONSTRAINT IF EXISTS policy_limits_max_per_tx_check,
    DROP CONSTRAINT IF EXISTS policy_limits_max_daily_amount_check,
    DROP CONSTRAINT IF EXISTS policy_limits_max_daily_count_check,
    DROP CONSTRAINT IF EXISTS policy_limits_max_monthly_amount_check,
    ADD CONSTRAINT policy_limits_max_per_tx_check CHECK (max_per_tx > 0),
    ADD CONSTRAINT policy_limits_max_daily_amount_check CHECK (max_daily_amount > 0),
    ADD CONSTRAINT policy_limits_max_daily_count_check CHECK (max_daily_count > 0),
    ADD CONSTRAINT policy_limits_max_monthly_amount_check CHECK (max_monthly_amount > 0);
ALTER TABLE policy_tier_limits
    DROP CONSTRAINT IF EXISTS policy_tier_limits_kyc_level_check,
    ADD CONSTRAINT policy_tier_limits_kyc_level_check CHECK (kyc_level IN (1, 2));
