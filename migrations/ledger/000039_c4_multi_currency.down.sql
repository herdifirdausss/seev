DROP TRIGGER IF EXISTS trg_entries_currency_guard ON ledger_entries;
DROP TRIGGER IF EXISTS trg_ltx_currency_guard ON ledger_transactions;
DROP TRIGGER IF EXISTS trg_fx_rate_overlap_guard ON fx_rate_versions;
DROP TRIGGER IF EXISTS trg_fx_rate_pair_guard ON fx_rate_versions;
DROP TRIGGER IF EXISTS trg_fx_rate_immutable_guard ON fx_rate_versions;
DROP TRIGGER IF EXISTS trg_fx_direction_pair_guard ON fx_pair_directions;
DROP TRIGGER IF EXISTS trg_fx_conversion_consistency_guard ON fx_conversions;
DROP TRIGGER IF EXISTS trg_fx_quote_consistency_guard ON fx_quotes;
DROP FUNCTION IF EXISTS fn_guard_ledger_entry_currency();
DROP FUNCTION IF EXISTS fn_guard_ledger_transaction_currency();
DROP FUNCTION IF EXISTS fn_guard_fx_rate_overlap();
DROP FUNCTION IF EXISTS fn_guard_fx_rate_pair();
DROP FUNCTION IF EXISTS fn_guard_fx_rate_immutable();
DROP FUNCTION IF EXISTS fn_guard_fx_direction_pair();
DROP FUNCTION IF EXISTS fn_guard_fx_conversion_consistency();
DROP FUNCTION IF EXISTS fn_guard_fx_quote_consistency();
DROP INDEX IF EXISTS idx_ltx_conversion_leg;

ALTER TABLE ledger_transactions
    DROP CONSTRAINT IF EXISTS ledger_transactions_fx_shape,
    DROP COLUMN IF EXISTS counterpart_transaction_id,
    DROP COLUMN IF EXISTS fx_leg,
    DROP COLUMN IF EXISTS fx_quote_id,
    DROP COLUMN IF EXISTS conversion_id;

ALTER TABLE disbursement_items
    DROP CONSTRAINT IF EXISTS disbursement_items_currency_shape,
    DROP COLUMN IF EXISTS currency;

DROP TABLE IF EXISTS fx_position_limits;
DROP TABLE IF EXISTS fx_conversions;
DROP TABLE IF EXISTS fx_quotes;
DROP TABLE IF EXISTS fx_rate_versions;
DROP TABLE IF EXISTS fx_pair_directions;
DROP TABLE IF EXISTS fx_pairs;

ALTER TABLE currencies
    DROP CONSTRAINT IF EXISTS currencies_status_check,
    DROP COLUMN IF EXISTS operations,
    DROP COLUMN IF EXISTS status;
