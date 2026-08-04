DROP POLICY IF EXISTS pol_read_readonly ON accounts;
DROP POLICY IF EXISTS pol_read_readonly ON account_balances;
DROP POLICY IF EXISTS pol_read_readonly ON ledger_transactions;
DROP POLICY IF EXISTS pol_read_readonly ON ledger_entries;
DROP POLICY IF EXISTS pol_read_readonly ON recon_batches;
DROP POLICY IF EXISTS pol_read_readonly ON recon_items;
DROP POLICY IF EXISTS pol_read_readonly ON account_balance_snapshots;

DROP POLICY IF EXISTS pol_all_service ON accounts;
DROP POLICY IF EXISTS pol_all_service ON account_balances;
DROP POLICY IF EXISTS pol_all_service ON ledger_transactions;
DROP POLICY IF EXISTS pol_all_service ON ledger_entries;
DROP POLICY IF EXISTS pol_all_service ON outbox_events;
DROP POLICY IF EXISTS pol_all_service ON pending_adjustments;
DROP POLICY IF EXISTS pol_all_service ON recon_batches;
DROP POLICY IF EXISTS pol_all_service ON recon_items;
DROP POLICY IF EXISTS pol_all_service ON account_balance_snapshots;

ALTER TABLE accounts                    NO FORCE ROW LEVEL SECURITY;
ALTER TABLE account_balances            NO FORCE ROW LEVEL SECURITY;
ALTER TABLE ledger_transactions         NO FORCE ROW LEVEL SECURITY;
ALTER TABLE ledger_entries              NO FORCE ROW LEVEL SECURITY;
ALTER TABLE outbox_events               NO FORCE ROW LEVEL SECURITY;
ALTER TABLE pending_adjustments         NO FORCE ROW LEVEL SECURITY;
ALTER TABLE recon_batches               NO FORCE ROW LEVEL SECURITY;
ALTER TABLE recon_items                 NO FORCE ROW LEVEL SECURITY;
ALTER TABLE account_balance_snapshots   NO FORCE ROW LEVEL SECURITY;

ALTER TABLE accounts                    DISABLE ROW LEVEL SECURITY;
ALTER TABLE account_balances            DISABLE ROW LEVEL SECURITY;
ALTER TABLE ledger_transactions         DISABLE ROW LEVEL SECURITY;
ALTER TABLE ledger_entries              DISABLE ROW LEVEL SECURITY;
ALTER TABLE outbox_events               DISABLE ROW LEVEL SECURITY;
ALTER TABLE pending_adjustments         DISABLE ROW LEVEL SECURITY;
ALTER TABLE recon_batches               DISABLE ROW LEVEL SECURITY;
ALTER TABLE recon_items                 DISABLE ROW LEVEL SECURITY;
ALTER TABLE account_balance_snapshots   DISABLE ROW LEVEL SECURITY;

REVOKE ALL PRIVILEGES ON
    accounts, account_balances, ledger_transactions, ledger_entries,
    outbox_events, pending_adjustments, recon_batches, recon_items,
    account_balance_snapshots, v_account_balance_audit
FROM app_service, app_readonly;

DROP ROLE IF EXISTS app_service;
DROP ROLE IF EXISTS app_readonly;
