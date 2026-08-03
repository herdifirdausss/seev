ALTER TABLE payin_topup_intents
    DROP CONSTRAINT IF EXISTS payin_topup_fee_snapshot_version_check,
    DROP CONSTRAINT IF EXISTS payin_topup_fee_application_check,
    DROP CONSTRAINT IF EXISTS payin_topup_total_debit_invariant,
    DROP CONSTRAINT IF EXISTS payin_topup_fee_amount_non_negative;
DROP INDEX IF EXISTS idx_payin_topup_intents_fee_quote;
DROP INDEX IF EXISTS uq_payin_topup_intents_fee_quote;
ALTER TABLE payin_topup_intents
    DROP COLUMN IF EXISTS fee_snapshot_version,
    DROP COLUMN IF EXISTS fee_quote_consumed_at,
    DROP COLUMN IF EXISTS fee_application,
    DROP COLUMN IF EXISTS total_debit,
    DROP COLUMN IF EXISTS fee_amount,
    DROP COLUMN IF EXISTS fee_gateway,
    DROP COLUMN IF EXISTS gateway,
    DROP COLUMN IF EXISTS fee_rule_id,
    DROP COLUMN IF EXISTS fee_quote_id;

ALTER TABLE payin_webhook_events
    DROP CONSTRAINT IF EXISTS payin_webhook_fee_snapshot_version_check,
    DROP CONSTRAINT IF EXISTS payin_webhook_fee_application_check,
    DROP CONSTRAINT IF EXISTS payin_webhook_total_debit_invariant,
    DROP CONSTRAINT IF EXISTS payin_webhook_fee_amount_non_negative;

ALTER TABLE payin_webhook_events
    DROP COLUMN IF EXISTS fee_snapshot_version,
    DROP COLUMN IF EXISTS fee_quote_consumed_at,
    DROP COLUMN IF EXISTS fee_application,
    DROP COLUMN IF EXISTS total_debit,
    DROP COLUMN IF EXISTS fee_amount,
    DROP COLUMN IF EXISTS fee_gateway,
    DROP COLUMN IF EXISTS fee_rule_id,
    DROP COLUMN IF EXISTS fee_quote_id;
