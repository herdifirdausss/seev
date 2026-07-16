DROP INDEX IF EXISTS idx_ltx_external_ref;
ALTER TABLE ledger_transactions
    DROP COLUMN IF EXISTS external_ref,
    DROP COLUMN IF EXISTS gateway;
