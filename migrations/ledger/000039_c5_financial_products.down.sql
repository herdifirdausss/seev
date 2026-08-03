-- C5 is rolled back only after the expand/contract evidence has proved that no
-- row is in use.  Keep this down migration intentionally conservative: it
-- removes only C5-owned rows/tables and never rewrites ledger history.
DROP TABLE IF EXISTS scheduled_execution_attempts;
DROP TABLE IF EXISTS scheduled_occurrences;

DROP TRIGGER IF EXISTS trg_c5_snapshot_immutable ON account_balance_snapshots;
DROP FUNCTION IF EXISTS fn_prevent_c5_snapshot_mutation();
DROP TRIGGER IF EXISTS trg_c5_closed_period_check_immutable ON interest_period_checks;

DROP INDEX IF EXISTS idx_fee_quotes_consumed_by_type;
ALTER TABLE fee_quotes
    DROP CONSTRAINT IF EXISTS fee_quotes_consumed_by_type_check,
    DROP COLUMN IF EXISTS consumed_by_type;

ALTER TABLE scheduled_transactions
    DROP COLUMN IF EXISTS command_type,
    DROP COLUMN IF EXISTS command_version,
    DROP COLUMN IF EXISTS command_digest,
    DROP COLUMN IF EXISTS currency,
    DROP COLUMN IF EXISTS timezone,
    DROP COLUMN IF EXISTS local_time,
    DROP COLUMN IF EXISTS missed_run_policy,
    DROP COLUMN IF EXISTS catch_up_limit,
    DROP COLUMN IF EXISTS max_fee_amount,
    DROP COLUMN IF EXISTS max_infrastructure_attempts,
    DROP COLUMN IF EXISTS retry_window_seconds,
    DROP COLUMN IF EXISTS fee_mode,
    DROP COLUMN IF EXISTS consecutive_failure_threshold,
    DROP COLUMN IF EXISTS consecutive_failure_count,
    DROP COLUMN IF EXISTS last_planned_at,
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS paused_reason;

DROP TABLE IF EXISTS interest_adjustments;
DROP TABLE IF EXISTS interest_period_checks;
DROP TABLE IF EXISTS interest_capitalization_items;
DROP TABLE IF EXISTS interest_daily_accruals;
DROP TABLE IF EXISTS interest_periods;
DROP INDEX IF EXISTS uq_savings_enrollments_active_product_account;
DROP TABLE IF EXISTS savings_enrollment_status_history;
DROP TABLE IF EXISTS savings_enrollments;
DROP TABLE IF EXISTS savings_rate_versions;
DROP TABLE IF EXISTS savings_products;

DROP FUNCTION IF EXISTS fn_guard_c5_product_terms();
DROP FUNCTION IF EXISTS fn_guard_c5_rate_terms();
DROP FUNCTION IF EXISTS fn_guard_c5_enrollment_identity();
DROP FUNCTION IF EXISTS fn_prevent_c5_closed_period_mutation();
DROP FUNCTION IF EXISTS fn_prevent_c5_closed_period_item_mutation();

DELETE FROM account_balances WHERE account_id IN (
    '00000000-0000-0000-0000-000000000031',
    '00000000-0000-0000-0000-000000000032'
);
DELETE FROM accounts WHERE id IN (
    '00000000-0000-0000-0000-000000000031',
    '00000000-0000-0000-0000-000000000032'
);

DROP INDEX IF EXISTS uq_account_balance_snapshots_id;
ALTER TABLE account_balance_snapshots DROP COLUMN IF EXISTS id;

ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check CHECK (type IN
    ('cash','hold','pending','frozen','pocket','fee',
     'settlement','escrow','chargeback','confiscated','adjustment','suspense',
     'fx_conversion','interest_expense'));
