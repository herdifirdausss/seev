-- Reverse of 000001_ledger_core.up.sql. Drop order respects FK dependencies;
-- triggers are dropped automatically with their owning table.

DROP VIEW IF EXISTS v_account_balance_audit;

DROP FUNCTION IF EXISTS fn_verify_account_balance(UUID);
DROP FUNCTION IF EXISTS fn_verify_ledger_balance(TIMESTAMPTZ, TIMESTAMPTZ);

DROP TABLE IF EXISTS outbox_events;
DROP FUNCTION IF EXISTS fn_outbox_check_dead_letter();

DROP TABLE IF EXISTS ledger_entries;
DROP FUNCTION IF EXISTS fn_prevent_entry_mutation();

DROP TABLE IF EXISTS ledger_transactions;
DROP TABLE IF EXISTS account_balances;
DROP TABLE IF EXISTS accounts;

DROP FUNCTION IF EXISTS fn_set_updated_at();

GRANT CREATE ON SCHEMA public TO PUBLIC;
