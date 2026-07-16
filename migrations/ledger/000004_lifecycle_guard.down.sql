ALTER TABLE ledger_transactions
    DROP CONSTRAINT IF EXISTS chk_closed_pair,
    DROP COLUMN IF EXISTS closed_reason,
    DROP COLUMN IF EXISTS closed_by_tx_id;
