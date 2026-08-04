-- Plan 46 T2: L0 is a hard policy control for users whose KYC level was
-- downgraded.  Zero limits fail closed even while an old JWT still claims
-- L1/L2; the token gate catches up after its short TTL.
ALTER TABLE policy_tier_limits
    DROP CONSTRAINT IF EXISTS policy_tier_limits_kyc_level_check,
    ADD CONSTRAINT policy_tier_limits_kyc_level_check CHECK (kyc_level IN (0, 1, 2));

-- Existing policy_limits accepted only positive values. L0 intentionally uses
-- zero as a hard deny, so widen the checks while retaining non-negative
-- validation for every pre-existing policy row.
ALTER TABLE policy_limits
    DROP CONSTRAINT IF EXISTS policy_limits_max_per_tx_check,
    DROP CONSTRAINT IF EXISTS policy_limits_max_daily_amount_check,
    DROP CONSTRAINT IF EXISTS policy_limits_max_daily_count_check,
    DROP CONSTRAINT IF EXISTS policy_limits_max_monthly_amount_check,
    ADD CONSTRAINT policy_limits_max_per_tx_check CHECK (max_per_tx >= 0),
    ADD CONSTRAINT policy_limits_max_daily_amount_check CHECK (max_daily_amount >= 0),
    ADD CONSTRAINT policy_limits_max_daily_count_check CHECK (max_daily_count >= 0),
    ADD CONSTRAINT policy_limits_max_monthly_amount_check CHECK (max_monthly_amount >= 0);

INSERT INTO policy_tier_limits
    (kyc_level, transaction_type, max_per_tx, max_daily_amount, max_daily_count, max_monthly_amount)
VALUES
    (0, 'transfer_p2p',       0, 0, 0, 0),
    (0, 'money_in',           0, 0, 0, 0),
    (0, 'withdraw_initiate',  0, 0, 0, 0)
ON CONFLICT (kyc_level, transaction_type) DO UPDATE SET
    max_per_tx = EXCLUDED.max_per_tx,
    max_daily_amount = EXCLUDED.max_daily_amount,
    max_daily_count = EXCLUDED.max_daily_count,
    max_monthly_amount = EXCLUDED.max_monthly_amount;
